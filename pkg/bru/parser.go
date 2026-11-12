package bru

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type BruBlock struct {
	Name    string
	SubName string
	Content []string
}

func ParseBruBlocks(raw string) []BruBlock {
	var blocks []BruBlock
	scanner := bufio.NewScanner(strings.NewReader(raw))
	
	var current *BruBlock
	inBlock := false

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if !inBlock {
			if strings.HasSuffix(trimmed, "{") {
				header := strings.TrimSpace(strings.TrimSuffix(trimmed, "{"))
				parts := strings.Fields(header)
				b := BruBlock{}
				if len(parts) > 0 {
					subParts := strings.SplitN(parts[0], ":", 2)
					b.Name = subParts[0]
					if len(subParts) > 1 {
						b.SubName = subParts[1]
					}
					if len(parts) > 1 && b.SubName == "" {
						b.SubName = parts[1]
					}
				}
				current = &b
				inBlock = true
			}
		} else {
			if trimmed == "}" {
				if current != nil {
					blocks = append(blocks, *current)
					current = nil
				}
				inBlock = false
			} else {
				if current != nil {
					current.Content = append(current.Content, line)
				}
			}
		}
	}

	return blocks
}

func ParseRequestFile(filePath, relPath string) (*BrunoRequest, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	raw := string(data)
	blocks := ParseBruBlocks(raw)

	filename := filepath.Base(filePath)
	name := strings.TrimSuffix(filename, ".bru")

	req := &BrunoRequest{
		ID:           relPath,
		Name:         name,
		Filename:     filename,
		FilePath:     filePath,
		RelativePath: relPath,
		Seq:          1,
		Method:       MethodGet,
		URL:          "",
		Headers:      []BruHeader{},
		Params:       []BruParam{},
		BodyType:     "none",
		BodyContent:  "",
		AuthType:     "inherit",
	}

	for _, block := range blocks {
		switch block.Name {
		case "meta":
			for _, line := range block.Content {
				k, v := parseKeyValue(line)
				if k == "name" && v != "" {
					req.Name = v
				} else if k == "seq" {
					if s, err := strconv.Atoi(v); err == nil {
						req.Seq = s
					}
				}
			}

		case "get", "post", "put", "patch", "delete", "options", "head":
			req.Method = HttpMethod(strings.ToUpper(block.Name))
			for _, line := range block.Content {
				k, v := parseKeyValue(line)
				if k == "url" {
					req.URL = v
				} else if k == "body" {
					req.BodyType = v
				} else if k == "auth" {
					req.AuthType = v
				}
			}

		case "headers":
			for _, line := range block.Content {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "//") {
					continue
				}
				colon := strings.Index(trimmed, ":")
				if colon != -1 {
					hName := strings.TrimSpace(trimmed[:colon])
					hVal := strings.TrimSpace(trimmed[colon+1:])
					req.Headers = append(req.Headers, BruHeader{
						Name:    hName,
						Value:   hVal,
						Enabled: true,
					})
				}
			}

		case "params":
			for _, line := range block.Content {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "//") {
					continue
				}
				eq := strings.Index(trimmed, "=")
				if eq != -1 {
					pName := strings.TrimSpace(trimmed[:eq])
					pVal := strings.TrimSpace(trimmed[eq+1:])
					req.Params = append(req.Params, BruParam{
						Name:    pName,
						Value:   pVal,
						Enabled: true,
					})
				}
			}

		case "body":
			req.BodyType = block.SubName
			req.BodyContent = strings.Join(block.Content, "\n")

		case "auth":
			if block.SubName == "bearer" {
				for _, line := range block.Content {
					k, v := parseKeyValue(line)
					if k == "token" {
						req.AuthBearerToken = v
					}
				}
			} else if block.SubName == "basic" {
				for _, line := range block.Content {
					k, v := parseKeyValue(line)
					if k == "username" {
						req.AuthBasicUser = v
					} else if k == "password" {
						req.AuthBasicPassword = v
					}
				}
			}

		case "script":
			if block.SubName == "pre-request" {
				req.PreRequestScript = strings.Join(block.Content, "\n")
			} else if block.SubName == "post-response" {
				req.PostResponseScript = strings.Join(block.Content, "\n")
			}

		case "docs":
			req.Docs = strings.Join(block.Content, "\n")
		}
	}

	return req, nil
}

func parseKeyValue(line string) (string, string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "//") {
		return "", ""
	}
	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	parts = strings.Fields(trimmed)
	if len(parts) >= 2 {
		return parts[0], strings.Join(parts[1:], " ")
	}
	return parts[0], ""
}

func SerializeRequest(req *BrunoRequest) string {
	var sb strings.Builder

	// meta
	sb.WriteString("meta {\n")
	sb.WriteString(fmt.Sprintf("  name: %s\n", req.Name))
	sb.WriteString("  type: http\n")
	sb.WriteString(fmt.Sprintf("  seq: %d\n", req.Seq))
	sb.WriteString("}\n\n")

	// method block
	m := strings.ToLower(string(req.Method))
	sb.WriteString(fmt.Sprintf("%s {\n", m))
	sb.WriteString(fmt.Sprintf("  url: %s\n", req.URL))
	sb.WriteString(fmt.Sprintf("  body: %s\n", req.BodyType))
	sb.WriteString(fmt.Sprintf("  auth: %s\n", req.AuthType))
	sb.WriteString("}\n\n")

	// headers
	if len(req.Headers) > 0 {
		sb.WriteString("headers {\n")
		for _, h := range req.Headers {
			if h.Enabled {
				sb.WriteString(fmt.Sprintf("  %s: %s\n", h.Name, h.Value))
			} else {
				sb.WriteString(fmt.Sprintf("  ~%s: %s\n", h.Name, h.Value))
			}
		}
		sb.WriteString("}\n\n")
	}

	// params
	if len(req.Params) > 0 {
		sb.WriteString("params {\n")
		for _, p := range req.Params {
			if p.Enabled {
				sb.WriteString(fmt.Sprintf("  %s: %s\n", p.Name, p.Value))
			} else {
				sb.WriteString(fmt.Sprintf("  ~%s: %s\n", p.Name, p.Value))
			}
		}
		sb.WriteString("}\n\n")
	}

	// auth details
	if req.AuthType == "bearer" && req.AuthBearerToken != "" {
		sb.WriteString("auth:bearer {\n")
		sb.WriteString(fmt.Sprintf("  token: %s\n", req.AuthBearerToken))
		sb.WriteString("}\n\n")
	} else if req.AuthType == "basic" {
		sb.WriteString("auth:basic {\n")
		sb.WriteString(fmt.Sprintf("  username: %s\n", req.AuthBasicUser))
		sb.WriteString(fmt.Sprintf("  password: %s\n", req.AuthBasicPassword))
		sb.WriteString("}\n\n")
	}

	// body
	if req.BodyType != "" && req.BodyType != "none" && req.BodyContent != "" {
		sb.WriteString(fmt.Sprintf("body:%s {\n", req.BodyType))
		sb.WriteString(req.BodyContent)
		if !strings.HasSuffix(req.BodyContent, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("}\n\n")
	}

	// scripts
	if req.PreRequestScript != "" {
		sb.WriteString("script:pre-request {\n")
		sb.WriteString(req.PreRequestScript)
		if !strings.HasSuffix(req.PreRequestScript, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("}\n\n")
	}
	if req.PostResponseScript != "" {
		sb.WriteString("script:post-response {\n")
		sb.WriteString(req.PostResponseScript)
		if !strings.HasSuffix(req.PostResponseScript, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("}\n\n")
	}

	// docs
	if req.Docs != "" {
		sb.WriteString("docs {\n")
		sb.WriteString(req.Docs)
		if !strings.HasSuffix(req.Docs, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("}\n")
	}

	return sb.String()
}
