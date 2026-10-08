package application

import (
	"awesomeProject/internal/config"
	"awesomeProject/internal/exclusion"
	"path"
	"strings"
)

// Exclusions use project-relative paths. Remote browsing uses server-root-relative
// paths, so only entries beneath a deployment mapping can match a project rule.
func remoteRootEntryExcluded(connection config.ProjectConnection, relative string) bool {
	relative = path.Clean(relative)
	projectPath, longest := "", -1
	for _, mapping := range connection.Mappings {
		base := path.Clean(mapping.DeploymentPath)
		var suffix string
		switch {
		case relative == base:
			suffix = "."
		case base == ".":
			suffix = relative
		case strings.HasPrefix(relative, base+"/"):
			suffix = strings.TrimPrefix(relative, base+"/")
		default:
			continue
		}
		length := len(base)
		if base == "." {
			length = 0
		}
		if length > longest {
			projectPath, longest = path.Join(mapping.LocalPath, suffix), length
		}
	}
	return longest >= 0 && exclusion.Match(connection.Excludes, projectPath)
}
