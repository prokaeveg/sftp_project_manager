package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/mapping"
	"awesomeProject/internal/transfer"
	"awesomeProject/internal/transport"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/pkg/sftp"
)

const (
	maxBatchItems = 10000
	maxBatchDepth = 64
)

type BatchItem struct {
	RelativePath  string `json:"relativePath"`
	Source        string `json:"source"`
	Destination   string `json:"destination"`
	Kind          string `json:"kind"`
	Size          int64  `json:"size"`
	Exists        bool   `json:"exists"`
	SkippedReason string `json:"skippedReason"`
}

type BatchPreview struct {
	Token      string      `json:"token"`
	Direction  string      `json:"direction"`
	Server     string      `json:"server"`
	Items      []BatchItem `json:"items"`
	TotalBytes int64       `json:"totalBytes"`
	FileCount  int         `json:"fileCount"`
}

type BatchItemResult struct {
	RelativePath string `json:"relativePath"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	Error        string `json:"error"`
}

type BatchResult struct {
	Items []BatchItemResult `json:"items"`
}

type batchPlannedItem struct {
	item   BatchItem
	source os.FileInfo
	target os.FileInfo // nil means absent at preview time
}

type batchPlan struct {
	preview BatchPreview
	items   []batchPlannedItem
}

// PreviewBatch enumerates once using a single SSH session and stores a private
// copy of both the connection and every planned operation. It never creates files.
func (s *Service) PreviewBatch(ctx context.Context, direction string, relatives []string) (BatchPreview, error) {
	if direction != "upload" && direction != "download" {
		return BatchPreview{}, errors.New("неизвестное направление передачи")
	}
	s.mu.Lock()
	if s.status.Settings.Profile == nil {
		s.mu.Unlock()
		return BatchPreview{}, errors.New("сначала сохраните профиль проекта")
	}
	profile := *s.status.Settings.Profile
	profile.Excludes = slices.Clone(profile.Excludes)
	projectID, connectionID := s.status.Settings.ActiveProjectID, s.status.Settings.ActiveConnectionID
	s.mu.Unlock()
	mapper, err := mapping.New(profile.LocalRoot, profile.RemoteRoot)
	if err != nil {
		return BatchPreview{}, err
	}
	selected, err := normalizeBatchSelection(mapper, relatives)
	if err != nil {
		return BatchPreview{}, err
	}
	for _, relative := range selected {
		if err := checkExcluded(profile, relative); err != nil {
			return BatchPreview{}, err
		}
	}
	var plan *batchPlan
	err = s.remoteOperation(ctx, profile, 2*time.Minute, func(operationCtx context.Context, session *transport.Session) error {
		var err error
		plan, err = buildBatch(operationCtx, profile, direction, selected, session.Client)
		return err
	})
	if err != nil {
		return BatchPreview{}, err
	}
	token, err := newID()
	if err != nil {
		return BatchPreview{}, err
	}
	plan.preview.Token = token
	s.mu.Lock()
	defer s.mu.Unlock()
	// A rule edit during enumeration must not resurrect a stale preview.
	policyUnchanged := false
	for _, project := range s.status.Settings.Projects {
		if project.ID == projectID {
			for _, connection := range project.Connections {
				if connection.ID == connectionID && slices.Equal(connection.Excludes, profile.Excludes) {
					policyUnchanged = true
				}
			}
		}
	}
	if !policyUnchanged {
		return BatchPreview{}, errors.New("исключения изменились во время предпросмотра; повторите его")
	}
	s.pending = &transferPlan{profile: profile, batch: plan}
	preview := plan.preview
	preview.Items = slices.Clone(preview.Items)
	return preview, nil
}

func normalizeBatchSelection(mapper *mapping.Mapper, relatives []string) ([]string, error) {
	if len(relatives) == 0 || len(relatives) > maxBatchItems {
		return nil, fmt.Errorf("выберите от 1 до %d элементов", maxBatchItems)
	}
	selected := make([]string, 0, len(relatives))
	for _, relative := range relatives {
		if _, err := mapper.RemoteRelativeToAbsolute(relative); err != nil {
			return nil, err
		}
		relative = path.Clean(relative)
		if len(strings.Split(relative, "/")) > maxBatchDepth {
			return nil, fmt.Errorf("слишком глубокий путь: %q", relative)
		}
		selected = append(selected, relative)
	}
	sort.Strings(selected)
	result := selected[:0]
	for _, relative := range selected {
		covered := false
		for _, parent := range result {
			if parent == "." || parent == relative || strings.HasPrefix(relative, parent+"/") {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, relative)
		}
	}
	return result, nil
}

type batchBuilder struct {
	ctx       context.Context
	profile   config.Profile
	direction string
	client    *sftp.Client
	items     map[string]batchPlannedItem
	casing    map[string]string
}

func buildBatch(ctx context.Context, profile config.Profile, direction string, relatives []string, client *sftp.Client) (*batchPlan, error) {
	mapper, err := mapping.New(profile.LocalRoot, profile.RemoteRoot)
	if err != nil {
		return nil, err
	}
	root, err := mapper.RemoteRelativeToAbsolute(".")
	if err != nil {
		return nil, err
	}
	if err := transfer.CheckRemotePath(client, root); err != nil {
		return nil, fmt.Errorf("корневая папка сервера: %w", err)
	}
	b := batchBuilder{ctx: ctx, profile: profile, direction: direction, client: client, items: map[string]batchPlannedItem{}, casing: map[string]string{}}
	for _, relative := range relatives {
		if err := b.walk(relative, true); err != nil {
			return nil, fmt.Errorf("%s: %w", relative, err)
		}
	}
	// A selected nested file can need parents outside the selected subtree.
	// Add only missing target directories, always within this mapping's root.
	var relativesWithParents []string
	for relative, planned := range b.items {
		if planned.item.SkippedReason == "" {
			relativesWithParents = append(relativesWithParents, relative)
		}
	}
	for _, relative := range relativesWithParents {
		for parent := path.Dir(relative); parent != "."; parent = path.Dir(parent) {
			if _, exists := b.items[parent]; exists {
				continue
			}
			local, remote, err := batchPaths(profile, parent)
			if err != nil {
				return nil, err
			}
			target, err := b.targetInfo(local, remote)
			if err != nil {
				return nil, err
			}
			if target != nil {
				if !realDirectory(target) {
					return nil, fmt.Errorf("папка назначения %q занята файлом или ссылкой", parent)
				}
				continue
			}
			if err := b.walk(parent, false); err != nil {
				return nil, err
			}
		}
	}
	server := profile.Connection.Alias
	if profile.Connection.Mode == "manual" {
		server = fmt.Sprintf("%s@%s:%d", profile.Connection.User, profile.Connection.Host, profile.Connection.Port)
	}
	plan := &batchPlan{preview: BatchPreview{Direction: direction, Server: server, Items: []BatchItem{}}}
	for _, item := range b.items {
		plan.items = append(plan.items, item)
	}
	sort.Slice(plan.items, func(i, j int) bool { return plan.items[i].item.RelativePath < plan.items[j].item.RelativePath })
	for _, planned := range plan.items {
		plan.preview.Items = append(plan.preview.Items, planned.item)
		if planned.item.Kind == "file" && planned.item.SkippedReason == "" {
			if planned.item.Size < 0 || plan.preview.TotalBytes > int64(^uint64(0)>>1)-planned.item.Size {
				return nil, errors.New("суммарный размер файлов слишком велик")
			}
			plan.preview.TotalBytes += planned.item.Size
			plan.preview.FileCount++
		}
	}
	return plan, nil
}

// batchPaths revalidates the root and all existing local components each time.
func batchPaths(profile config.Profile, relative string) (local, remote string, err error) {
	mapper, err := mapping.New(profile.LocalRoot, profile.RemoteRoot)
	if err != nil {
		return "", "", err
	}
	remote, err = mapper.RemoteRelativeToAbsolute(relative)
	if err != nil {
		return "", "", err
	}
	local, err = mapper.RemoteToLocal(remote)
	return local, remote, err
}

func realDirectory(info os.FileInfo) bool {
	return info.IsDir() && info.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0
}

func (b *batchBuilder) walk(relative string, recurse bool) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if len(strings.Split(relative, "/")) > maxBatchDepth {
		return fmt.Errorf("превышена глубина %d папок", maxBatchDepth)
	}
	if _, exists := b.items[relative]; exists {
		return nil
	}
	if len(b.items) >= maxBatchItems {
		return fmt.Errorf("план превышает %d элементов; выберите меньшую папку", maxBatchItems)
	}
	// Validate ancestors before inspecting a leaf; links themselves are reported
	// as skipped and are never followed, including explicitly selected links.
	if relative != "." {
		_, parentRemote, err := batchPaths(b.profile, path.Dir(relative))
		if err != nil {
			return err
		}
		if b.direction == "download" {
			if err := transfer.CheckRemotePath(b.client, parentRemote); err != nil {
				return err
			}
		}
	}
	local := filepath.Join(b.profile.LocalRoot, filepath.FromSlash(relative))
	remote := path.Join(b.profile.RemoteRoot, relative)
	var source os.FileInfo
	var err error
	if b.direction == "upload" {
		source, err = os.Lstat(local)
	} else {
		source, err = b.client.Lstat(remote)
	}
	if err != nil {
		return err
	}
	item := BatchItem{RelativePath: relative, Source: local, Destination: remote, Kind: "file", Size: source.Size()}
	if b.direction == "download" {
		item.Source, item.Destination = remote, local
	}
	if source.IsDir() {
		item.Kind, item.Size = "directory", 0
	}
	switch {
	case excluded(b.profile, relative):
		item.SkippedReason = "исключён правилами подключения"
	case source.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0:
		item.SkippedReason = "ссылка или junction: передача запрещена"
	case !source.Mode().IsRegular() && !source.IsDir():
		item.SkippedReason = "не является обычным файлом или папкой"
	}
	if item.SkippedReason != "" {
		b.items[relative] = batchPlannedItem{item: item, source: source}
		return nil
	}
	local, remote, err = batchPaths(b.profile, relative)
	if err != nil {
		return err
	}
	if err := b.checkCasing(relative); err != nil {
		return err
	}
	target, err := b.targetInfo(local, remote)
	if err != nil {
		return err
	}
	if target != nil {
		if source.IsDir() && !realDirectory(target) || !source.IsDir() && !target.Mode().IsRegular() || target.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("несовместимый файл, папка или ссылка в назначении %q", relative)
		}
		item.Exists = true
	}
	b.items[relative] = batchPlannedItem{item: item, source: source, target: target}
	if source.IsDir() && recurse {
		var names []string
		if b.direction == "upload" {
			entries, err := os.ReadDir(local)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
		} else {
			if err := transfer.CheckRemotePath(b.client, remote); err != nil {
				return err
			}
			entries, err := b.client.ReadDir(remote)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
				return errors.New("сервер вернул недопустимое имя файла")
			}
			if err := b.walk(path.Join(relative, name), true); err != nil {
				return fmt.Errorf("%s: %w", path.Join(relative, name), err)
			}
		}
	}
	return nil
}

func (b *batchBuilder) checkCasing(relative string) error {
	// Check every prefix, including directories not selected themselves. This
	// catches foo/a and Foo/b even when neither target directory exists yet.
	for current := relative; current != "."; current = path.Dir(current) {
		key := foldBatchPath(current)
		if previous, exists := b.casing[key]; exists && previous != current {
			return fmt.Errorf("пути %q и %q конфликтуют по регистру в Windows", previous, current)
		}
		b.casing[key] = current
	}
	return nil
}

// Canonicalize the complete SimpleFold cycle so the map has EqualFold
// semantics, including Unicode names such as the two lowercase sigmas.
func foldBatchPath(value string) string {
	return strings.Map(func(r rune) rune {
		minimum := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		return minimum
	}, value)
}

func (b *batchBuilder) targetInfo(local, remote string) (os.FileInfo, error) {
	if b.direction == "download" {
		info, err := os.Lstat(local)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return info, err
	}
	// Inspect each component before addressing deeper paths. A missing parent
	// is valid for a preview; a symbolic link or a file parent is never valid.
	relative := strings.TrimPrefix(strings.TrimPrefix(remote, path.Clean(b.profile.RemoteRoot)), "/")
	current := path.Clean(b.profile.RemoteRoot)
	if relative == "" {
		return b.client.Lstat(current)
	}
	parts := strings.Split(relative, "/")
	for i, part := range parts {
		current = path.Join(current, part)
		info, err := b.client.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if i == len(parts)-1 {
			return info, nil
		}
		if !realDirectory(info) {
			return nil, fmt.Errorf("предок назначения %q не является обычной папкой", current)
		}
	}
	return nil, errors.New("недопустимый путь назначения")
}

func sameBatchMetadata(before, after os.FileInfo, local bool) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return before.Mode() == after.Mode() && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) && (!local || os.SameFile(before, after))
}

func (s *Service) ExecuteBatch(ctx context.Context, token, existingPolicy string, includeEnvironment bool) (BatchResult, error) {
	if existingPolicy == "" {
		existingPolicy = "skip"
	}
	if existingPolicy != "skip" && existingPolicy != "overwrite" {
		return BatchResult{}, errors.New("выберите политику skip или overwrite")
	}
	s.mu.Lock()
	if s.pending == nil || s.pending.batch == nil || s.pending.batch.preview.Token != token {
		s.mu.Unlock()
		return BatchResult{}, errors.New("предпросмотр устарел; повторите его")
	}
	if !includeEnvironment {
		for _, planned := range s.pending.batch.items {
			name := strings.ToLower(path.Base(planned.item.RelativePath))
			if planned.item.Kind == "file" && planned.item.SkippedReason == "" && (name == ".env" || strings.HasPrefix(name, ".env.")) {
				s.mu.Unlock()
				return BatchResult{}, errors.New("для передачи .env требуется явное подтверждение")
			}
		}
	}
	plan := *s.pending
	s.pending = nil // one-shot, also when connecting or executing fails
	s.mu.Unlock()
	var result BatchResult
	started := false
	err := s.remoteOperation(ctx, plan.profile, 30*time.Minute, func(operationCtx context.Context, session *transport.Session) error {
		started = true
		result = executeBatch(operationCtx, plan.profile, plan.batch, existingPolicy, session.Client)
		return nil // partial and cancelled results must survive the Wails bridge
	})
	if started {
		return result, nil
	}
	return result, err
}

func executeBatch(ctx context.Context, profile config.Profile, plan *batchPlan, policy string, client *sftp.Client) BatchResult {
	result := BatchResult{Items: make([]BatchItemResult, 0, len(plan.items))}
	blocked := map[string]bool{}
	for _, planned := range plan.items {
		item := planned.item
		entry := BatchItemResult{RelativePath: item.RelativePath, Kind: item.Kind}
		if err := ctx.Err(); err != nil {
			entry.Status, entry.Error = "cancelled", err.Error()
		} else if item.SkippedReason != "" {
			entry.Status, entry.Error = "skipped", item.SkippedReason
		} else {
			var err error
			for parent := path.Dir(item.RelativePath); ; parent = path.Dir(parent) {
				if blocked[parent] {
					err = errors.New("родительская папка не прошла проверку или не была создана")
					break
				}
				if parent == "." {
					break
				}
			}
			if err == nil {
				err = executeBatchItem(ctx, profile, plan.preview.Direction, planned, policy, client)
			}
			switch {
			case err == nil:
				// A successful commit remains completed even if cancellation arrives
				// immediately after the file was installed or directory was created.
				entry.Status = "completed"
			case errors.Is(err, errBatchSkipped):
				entry.Status, entry.Error = "skipped", "файл назначения существует; выбран пропуск"
			case ctx.Err() != nil:
				entry.Status, entry.Error = "cancelled", errors.Join(ctx.Err(), err).Error()
			default:
				entry.Status, entry.Error = "failed", err.Error()
				if item.Kind == "directory" {
					blocked[item.RelativePath] = true
				}
			}
		}
		result.Items = append(result.Items, entry)
	}
	return result
}

var errBatchSkipped = errors.New("existing destination skipped")

func executeBatchItem(ctx context.Context, profile config.Profile, direction string, planned batchPlannedItem, policy string, client *sftp.Client) error {
	item := planned.item
	if err := checkExcluded(profile, item.RelativePath); err != nil {
		return err
	}
	local, remote, err := batchPaths(profile, item.RelativePath)
	if err != nil {
		return err
	}
	// Validate both roots even for a single selected leaf whose parents exist.
	if err := transfer.CheckRemotePath(client, path.Clean(profile.RemoteRoot)); err != nil {
		return err
	}
	if item.RelativePath != "." {
		if err := transfer.CheckRemotePath(client, path.Dir(remote)); err != nil {
			return err
		}
	}
	var source, target os.FileInfo
	if direction == "upload" {
		source, err = os.Lstat(local)
	} else {
		source, err = client.Lstat(remote)
	}
	if err != nil {
		return err
	}
	if !sameBatchMetadata(planned.source, source, direction == "upload") {
		return errors.New("источник изменился после предпросмотра; повторите его")
	}
	if direction == "upload" {
		target, err = client.Lstat(remote)
	} else {
		target, err = os.Lstat(local)
	}
	if errors.Is(err, os.ErrNotExist) {
		target, err = nil, nil
	}
	if err != nil {
		return err
	}
	if !sameBatchMetadata(planned.target, target, direction == "download") {
		return errors.New("назначение изменилось после предпросмотра; повторите его")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if item.Kind == "directory" {
		if target != nil {
			return nil
		}
		// Only this exact planned directory is created. Its parents already exist
		// and have passed mapper and remote-chain checks; never use MkdirAll.
		if direction == "upload" {
			return client.Mkdir(remote)
		}
		return os.Mkdir(local, 0700)
	}
	if target != nil && policy == "skip" {
		return errBatchSkipped
	}
	overwrite := target != nil && policy == "overwrite"
	if direction == "upload" {
		return transfer.Upload(ctx, client, local, remote, overwrite)
	}
	return transfer.Download(ctx, client, remote, local, overwrite)
}
