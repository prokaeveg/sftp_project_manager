package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/transport"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Only these project-level files are read. No directory traversal or SSH evaluation.
type PhpStormPreview struct {
	Project            config.Project `json:"project"`
	Warnings           []string       `json:"warnings"`
	AliasConnectionIDs []string       `json:"aliasConnectionIDs"`
}
type ideaProject struct {
	XMLName    xml.Name        `xml:"project"`
	Components []ideaComponent `xml:"component"`
}
type ideaComponent struct {
	Name       string       `xml:"name,attr"`
	ServerName string       `xml:"serverName,attr"`
	Paths      []ideaPaths  `xml:"serverData>paths"`
	Servers    []ideaServer `xml:"option>webServer"`
	SSH        []ideaSSH    `xml:"configs>sshConfig"`
}
type ideaPaths struct {
	Name     string        `xml:"name,attr"`
	Mappings []ideaMapping `xml:"serverdata>mappings>mapping"`
	Excluded []struct{}    `xml:"serverdata>excludedPaths>excludedPath"`
}
type ideaMapping struct {
	Local  string `xml:"local,attr"`
	Deploy string `xml:"deploy,attr"`
}
type ideaServer struct {
	Name     string       `xml:"name,attr"`
	Transfer ideaTransfer `xml:"fileTransfer"`
}
type ideaTransfer struct {
	Access string `xml:"accessType,attr"`
	Host   string `xml:"host,attr"`
	Port   string `xml:"port,attr"`
	User   string `xml:"username,attr"`
	Root   string `xml:"rootFolder,attr"`
	SSHID  string `xml:"sshConfigId,attr"`
	Auth   string `xml:"authType,attr"`
	Key    string `xml:"privateKey,attr"`
}
type ideaSSH struct {
	ID      string `xml:"id,attr"`
	Host    string `xml:"host,attr"`
	Port    string `xml:"port,attr"`
	User    string `xml:"username,attr"`
	Auth    string `xml:"authType,attr"`
	Key     string `xml:"privateKey,attr"`
	OpenSSH bool   `xml:"useOpenSSHConfig,attr"`
}

func readIdeaXML(root, name string, optional bool) (ideaProject, error) {
	var doc ideaProject
	f, err := os.Open(filepath.Join(root, ".idea", name))
	if optional && errors.Is(err, os.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return doc, fmt.Errorf("не удалось прочитать .idea/%s", name)
	}
	defer f.Close()
	const limit = 2 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(data) > limit {
		return doc, fmt.Errorf(".idea/%s: ошибка чтения или размер больше 2 МиБ", name)
	}
	// XML errors are deliberately not echoed: source attributes can contain secrets.
	decoder := xml.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&doc) != nil {
		return ideaProject{}, fmt.Errorf(".idea/%s: некорректный XML проекта", name)
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ideaProject{}, fmt.Errorf(".idea/%s: некорректный XML проекта", name)
		}
		switch value := token.(type) {
		case xml.Comment:
		case xml.CharData:
			if len(bytes.TrimSpace(value)) == 0 {
				continue
			}
			return ideaProject{}, fmt.Errorf(".idea/%s: лишние данные после XML проекта", name)
		default:
			return ideaProject{}, fmt.Errorf(".idea/%s: лишние данные после XML проекта", name)
		}
	}
	return doc, nil
}

func sameProjectRoot(a, b string) bool {
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	return localPathContains(a, b) && localPathContains(b, a)
}

func (s *Service) PreviewPhpStormProject(root string) (PhpStormPreview, error) {
	result, err := previewPhpStormProject(root)
	if err != nil {
		return PhpStormPreview{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.status.Settings.Projects {
		if sameProjectRoot(existing.LocalRoot, result.Project.LocalRoot) {
			return PhpStormPreview{}, errors.New("проект с этой локальной папкой уже существует")
		}
	}
	return result, nil
}

func previewPhpStormProject(root string) (PhpStormPreview, error) {
	result := PhpStormPreview{Warnings: []string{}, AliasConnectionIDs: []string{}}
	if !filepath.IsAbs(root) {
		return result, errors.New("укажите абсолютный путь к одному проекту PhpStorm")
	}
	root = filepath.Clean(root)
	if filepath.Base(root) == ".idea" {
		root = filepath.Dir(root)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return result, errors.New("папка проекта недоступна")
	}
	deploy, err := readIdeaXML(root, "deployment.xml", false)
	if err != nil {
		return result, err
	}
	servers, err := readIdeaXML(root, "webServers.xml", true)
	if err != nil {
		return result, err
	}
	ssh, err := readIdeaXML(root, "sshConfigs.xml", true)
	if err != nil {
		return result, err
	}
	result.Project = config.Project{Name: filepath.Base(root), LocalRoot: root, Connections: []config.ProjectConnection{}}
	sshByID := map[string]ideaSSH{}
	for _, component := range ssh.Components {
		if component.Name == "SshConfigs" {
			for _, c := range component.SSH {
				sshByID[c.ID] = c
			}
		}
	}
	pathsByName := map[string]ideaPaths{}
	defaultName := ""
	for _, component := range deploy.Components {
		if component.Name == "PublishConfigData" {
			defaultName = component.ServerName
			for _, p := range component.Paths {
				pathsByName[p.Name] = p
			}
		}
	}
	seen := map[string]bool{}
	for _, component := range servers.Components {
		if component.Name != "WebServers" {
			continue
		}
		for _, server := range component.Servers {
			if seen[server.Name] {
				result.Warnings = append(result.Warnings, "Повторяющееся имя сервера пропущено: "+server.Name)
				continue
			}
			seen[server.Name] = true
			connection, alias, err := importIdeaConnection(root, server, pathsByName[server.Name], sshByID)
			if err != nil {
				result.Warnings = append(result.Warnings, "Сервер «"+server.Name+"» пропущен: "+err.Error())
				continue
			}
			result.Project.Connections = append(result.Project.Connections, connection)
			if alias {
				result.AliasConnectionIDs = append(result.AliasConnectionIDs, connection.ID)
				result.Warnings = append(result.Warnings, "«"+server.Name+"»: используется OpenSSH config. Подтвердите SSH-алиас явно; команды доверенного SSH-конфига могут выполняться при подключении.")
			}
			if server.Name == defaultName {
				result.Project.DefaultConnectionID = connection.ID
			}
			if len(pathsByName[server.Name].Excluded) > 0 {
				result.Warnings = append(result.Warnings, "«"+server.Name+"»: исключения PhpStorm не перенесены. Настройте их перед передачей.")
			}
		}
	}
	for name := range pathsByName {
		if !seen[name] {
			result.Warnings = append(result.Warnings, "«"+name+"»: описание сервера отсутствует в project-level webServers.xml. Настройки уровня IDE не читаются.")
		}
	}
	if len(result.Project.Connections) == 0 {
		result.Warnings = append(result.Warnings, "Поддерживаемых SFTP-серверов не найдено. Можно создать локальный проект.")
	}
	result.Warnings = append(result.Warnings, "Пароли, passphrase, автозагрузка и сохранённое доверие ключам не импортируются. Подключения не выполнялись.")
	return result, nil
}

func importIdeaConnection(root string, server ideaServer, paths ideaPaths, configs map[string]ideaSSH) (config.ProjectConnection, bool, error) {
	t := server.Transfer
	c := config.ProjectConnection{Name: server.Name, RootPath: t.Root, Mappings: []config.Mapping{}, Excludes: []string{".git/**", "**/node_modules/**", "**/.env", "**/.env.*"}}
	if t.Access != "SFTP" {
		return c, false, errors.New("поддерживается только SFTP")
	}
	if c.RootPath == "" {
		c.RootPath = "/"
	}
	source := ideaSSH{Host: t.Host, Port: t.Port, User: t.User, Auth: t.Auth, Key: t.Key}
	if t.SSHID != "" {
		var ok bool
		source, ok = configs[t.SSHID]
		if !ok {
			return c, false, errors.New("SSH-конфигурация отсутствует в project-level sshConfigs.xml")
		}
	}
	port := 22
	if source.Port != "" {
		var err error
		port, err = strconv.Atoi(source.Port)
		if err != nil {
			return c, false, errors.New("некорректный SSH-порт")
		}
	}
	c.Connection = transport.Connection{Mode: "manual", Host: source.Host, User: source.User, Port: port}
	if source.OpenSSH {
		c.Connection = transport.Connection{Mode: "ssh_alias", Alias: source.Host}
		if source.Auth == "PASSWORD" {
			c.Connection.AuthMode = "password"
		}
	} else {
		switch source.Auth {
		case "PASSWORD":
			c.Connection.AuthMode = "password"
		case "KEY_PAIR":
			key := strings.ReplaceAll(source.Key, "$PROJECT_DIR$", root)
			if key == "" || strings.Contains(key, "$") || !filepath.IsAbs(key) {
				return c, false, errors.New("путь SSH-ключа не поддерживается; настройте сервер вручную")
			}
			c.Connection.IdentityFile = filepath.Clean(key)
		case "", "OPEN_SSH", "AGENT":
			c.Connection.AuthMode = "key_agent"
		default:
			return c, false, errors.New("неподдерживаемый способ авторизации")
		}
	}
	var err error
	c.ID, err = newID()
	if err != nil {
		return c, false, err
	}
	for _, m := range paths.Mappings {
		local := strings.ReplaceAll(m.Local, "$PROJECT_DIR$", root)
		if strings.Contains(local, "$") || !filepath.IsAbs(local) {
			return c, false, errors.New("неподдерживаемый локальный путь mapping")
		}
		relative, err := filepath.Rel(root, filepath.Clean(local))
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return c, false, errors.New("mapping вне выбранного проекта")
		}
		id, err := newID()
		if err != nil {
			return c, false, err
		}
		deployment := strings.TrimPrefix(m.Deploy, "/")
		if deployment == "" {
			deployment = "."
		}
		c.Mappings = append(c.Mappings, config.Mapping{ID: id, LocalPath: filepath.ToSlash(relative), DeploymentPath: deployment})
	}
	// Reuse the application's full path, overlap, SSH and mapping validation.
	if err := validateProject(config.Project{Name: "Import", LocalRoot: root, Connections: []config.ProjectConnection{c}}); err != nil {
		return c, false, errors.New("неполные или некорректные параметры SSH/mapping; настройте сервер вручную")
	}
	return c, source.OpenSSH, nil
}
