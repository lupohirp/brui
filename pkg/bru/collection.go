package bru

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func LoadCollection(rootPath string) (*BrunoCollection, error) {
	absPath, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}

	collName := filepath.Base(absPath)
	coll := &BrunoCollection{
		Name:              collName,
		Path:              absPath,
		Folders:           []*BrunoFolder{},
		Requests:          []*BrunoRequest{},
		Environments:      []*BrunoEnvironment{},
		RuntimeVariables:  make(map[string]any),
		CollectionHeaders: []BruHeader{},
		CollectionAuthType: "none",
	}

	// 1. Check collection.bru
	collBruPath := filepath.Join(absPath, "collection.bru")
	if _, err := os.Stat(collBruPath); err == nil {
		parseCollectionBru(collBruPath, coll)
	}

	// 2. Check bruno.json
	brunoJSONPath := filepath.Join(absPath, "bruno.json")
	if data, err := os.ReadFile(brunoJSONPath); err == nil {
		var bj struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &bj) == nil && bj.Name != "" {
			coll.Name = bj.Name
		}
	}

	// 3. Load environments from environments/
	envDir := filepath.Join(absPath, "environments")
	if entries, err := os.ReadDir(envDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".bru") {
				envName := strings.TrimSuffix(e.Name(), ".bru")
				envPath := filepath.Join(envDir, e.Name())
				env := parseEnvironmentFile(envPath, envName)
				coll.Environments = append(coll.Environments, env)
			}
		}
	}

	// Default active environment
	if len(coll.Environments) > 0 {
		coll.ActiveEnvironment = coll.Environments[0].Name
	}

	// 4. Discover folders and requests recursively
	coll.Folders, coll.Requests = scanFolder(absPath, absPath)

	return coll, nil
}

func parseCollectionBru(filePath string, coll *BrunoCollection) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return
	}
	blocks := ParseBruBlocks(string(data))
	for _, b := range blocks {
		switch b.Name {
		case "meta":
			for _, line := range b.Content {
				k, v := parseKeyValue(line)
				if k == "name" && v != "" {
					coll.Name = v
				}
			}
		case "headers":
			for _, line := range b.Content {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "//") {
					continue
				}
				colon := strings.Index(trimmed, ":")
				if colon != -1 {
					coll.CollectionHeaders = append(coll.CollectionHeaders, BruHeader{
						Name:    strings.TrimSpace(trimmed[:colon]),
						Value:   strings.TrimSpace(trimmed[colon+1:]),
						Enabled: true,
					})
				}
			}
		case "auth":
			if b.SubName == "bearer" {
				for _, line := range b.Content {
					k, v := parseKeyValue(line)
					if k == "token" {
						coll.CollectionAuthBearerToken = v
					}
				}
			} else {
				for _, line := range b.Content {
					k, v := parseKeyValue(line)
					if k == "mode" {
						coll.CollectionAuthType = v
					}
				}
			}
		case "script":
			if b.SubName == "pre-request" {
				coll.CollectionPreRequestScript = strings.Join(b.Content, "\n")
			}
		}
	}
}

func parseEnvironmentFile(filePath, name string) *BrunoEnvironment {
	env := &BrunoEnvironment{
		Name:      name,
		FilePath:  filePath,
		Variables: make(map[string]string),
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return env
	}

	blocks := ParseBruBlocks(string(data))
	for _, b := range blocks {
		if b.Name == "vars" {
			for _, line := range b.Content {
				k, v := parseKeyValue(line)
				if k != "" {
					env.Variables[k] = v
				}
			}
		}
	}
	return env
}

func scanFolder(folderPath, rootPath string) ([]*BrunoFolder, []*BrunoRequest) {
	var folders []*BrunoFolder
	var requests []*BrunoRequest

	entries, err := os.ReadDir(folderPath)
	if err != nil {
		return folders, requests
	}

	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" || name == "environments" {
			continue
		}

		fullPath := filepath.Join(folderPath, name)
		relPath, _ := filepath.Rel(rootPath, fullPath)

		if e.IsDir() {
			subFolders, subRequests := scanFolder(fullPath, rootPath)
			f := &BrunoFolder{
				ID:           relPath,
				Name:         name,
				Path:         fullPath,
				RelativePath: relPath,
				Folders:      subFolders,
				Requests:     subRequests,
			}
			folders = append(folders, f)
		} else if strings.HasSuffix(name, ".bru") && name != "collection.bru" && name != "folder.bru" {
			req, err := ParseRequestFile(fullPath, relPath)
			if err == nil {
				requests = append(requests, req)
			}
		}
	}

	// Sort folders and requests
	sort.Slice(folders, func(i, j int) bool {
		return folders[i].Name < folders[j].Name
	})
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].Seq != requests[j].Seq {
			return requests[i].Seq < requests[j].Seq
		}
		return requests[i].Name < requests[j].Name
	})

	return folders, requests
}

func SerializeCollectionBru(coll *BrunoCollection) string {
	var sb strings.Builder

	sb.WriteString("meta {\n")
	sb.WriteString(fmt.Sprintf("  name: %s\n", coll.Name))
	sb.WriteString("  type: collection\n")
	sb.WriteString("}\n\n")

	if len(coll.CollectionHeaders) > 0 {
		sb.WriteString("headers {\n")
		for _, h := range coll.CollectionHeaders {
			if h.Enabled {
				sb.WriteString(fmt.Sprintf("  %s: %s\n", h.Name, h.Value))
			} else {
				sb.WriteString(fmt.Sprintf("  ~%s: %s\n", h.Name, h.Value))
			}
		}
		sb.WriteString("}\n\n")
	}

	if coll.CollectionAuthType != "" && coll.CollectionAuthType != "none" {
		sb.WriteString("auth {\n")
		sb.WriteString(fmt.Sprintf("  mode: %s\n", coll.CollectionAuthType))
		sb.WriteString("}\n\n")

		if coll.CollectionAuthType == "bearer" && coll.CollectionAuthBearerToken != "" {
			sb.WriteString("auth:bearer {\n")
			sb.WriteString(fmt.Sprintf("  token: %s\n", coll.CollectionAuthBearerToken))
			sb.WriteString("}\n\n")
		}
	}

	if coll.CollectionPreRequestScript != "" {
		sb.WriteString("script:pre-request {\n")
		sb.WriteString(coll.CollectionPreRequestScript)
		if !strings.HasSuffix(coll.CollectionPreRequestScript, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("}\n")
	}

	return sb.String()
}
