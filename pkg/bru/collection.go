package bru

import (
	"encoding/json"
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
		Name:               collName,
		Path:               absPath,
		Folders:            []*BrunoFolder{},
		Requests:           []*BrunoRequest{},
		Environments:       []*BrunoEnvironment{},
		RuntimeVariables:   make(map[string]any),
		CollectionHeaders:  []BruHeader{},
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
	coll.collectionBlocks = blocks
	coll.collectionTail = extraTrailingNewlines(string(data))
	for _, b := range blocks {
		switch b.key() {
		case "meta":
			for _, line := range b.Content {
				k, v := parseKeyValue(line)
				if k == "name" && v != "" {
					coll.Name = v
				}
			}
		case "headers":
			for _, line := range b.Content {
				if e, ok := parseDictLine(line); ok {
					coll.CollectionHeaders = append(coll.CollectionHeaders, BruHeader{Name: e.Key, Value: e.Value, Enabled: e.Enabled})
				}
			}
		case "auth":
			for _, line := range b.Content {
				if k, v := parseKeyValue(line); k == "mode" {
					coll.CollectionAuthType = v
				}
			}
		case "auth:bearer":
			for _, line := range b.Content {
				if k, v := parseKeyValue(line); k == "token" {
					coll.CollectionAuthBearerToken = v
				}
			}
		case "auth:oauth2":
			coll.CollectionAuthOAuth2 = parseOAuth2Block(b.Content)
		case "script:pre-request":
			coll.CollectionPreRequestScript = dedent(b.Content)
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

	env.rawBlocks = ParseBruBlocks(string(data))
	env.rawTail = extraTrailingNewlines(string(data))
	for _, b := range env.rawBlocks {
		if b.key() != "vars" {
			continue
		}
		for _, line := range b.Content {
			// Disabled (~) variables are not active; they are kept on disk via rawBlocks.
			if e, ok := parseDictLine(line); ok && e.Enabled && e.Key != "" {
				env.Variables[e.Key] = e.Value
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

// SerializeCollectionBru renders collection.bru, regenerating only headers,
// auth mode, bearer token and the pre-request script; every other block
// (meta, auth:oauth2, vars, tests, docs, ...) is written back verbatim.
func SerializeCollectionBru(coll *BrunoCollection) string {
	orig := func(key string) *BruBlock {
		for i := range coll.collectionBlocks {
			if coll.collectionBlocks[i].key() == key {
				return &coll.collectionBlocks[i]
			}
		}
		return nil
	}
	m := newManagedBlocks()

	var headers []dictEntry
	for _, h := range coll.CollectionHeaders {
		headers = append(headers, dictEntry{Key: h.Name, Value: h.Value, Enabled: h.Enabled})
	}
	m.setDict("headers", orig("headers"), headers)

	mode := coll.CollectionAuthType
	if mode == "" {
		mode = "none"
	}
	if o := orig("auth"); o != nil || mode != "none" {
		var lines []string
		if o != nil {
			lines = o.Content
		}
		m.set("auth", &BruBlock{Header: "auth", Content: setKV(lines, "mode", mode)})
	}
	if coll.CollectionAuthBearerToken != "" {
		var lines []string
		if o := orig("auth:bearer"); o != nil {
			lines = o.Content
		}
		m.set("auth:bearer", &BruBlock{Header: "auth:bearer", Content: setKV(lines, "token", coll.CollectionAuthBearerToken)})
	} else {
		m.set("auth:bearer", nil)
	}
	m.setText("script:pre-request", orig("script:pre-request"), coll.CollectionPreRequestScript)

	return m.render(coll.collectionBlocks, BruBlock.key) + coll.collectionTail
}

// SerializeEnvironment renders an environment file. Existing variables keep
// their position, disabled (~) variables and other blocks such as
// `vars:secret [...]` are preserved, and new variables are appended sorted.
func SerializeEnvironment(env *BrunoEnvironment) string {
	var lines []string
	emitted := map[string]bool{}
	for _, b := range env.rawBlocks {
		if b.key() != "vars" {
			continue
		}
		for _, line := range b.Content {
			e, ok := parseDictLine(line)
			if !ok || !e.Enabled || e.Key == "" {
				lines = append(lines, line) // comments, blanks, disabled vars
				continue
			}
			if v, exists := env.Variables[e.Key]; exists && !emitted[e.Key] {
				if v == e.Value {
					lines = append(lines, line) // unchanged: keep the exact original line
				} else {
					lines = append(lines, formatDictLine(e.Key, v, true))
				}
				emitted[e.Key] = true
			}
		}
		break
	}
	for _, k := range sortedKeys(env.Variables) {
		if !emitted[k] {
			lines = append(lines, formatDictLine(k, env.Variables[k], true))
		}
	}

	m := newManagedBlocks()
	m.set("vars", &BruBlock{Header: "vars", Content: lines})
	return m.render(env.rawBlocks, BruBlock.key) + env.rawTail
}
