package bru

type HttpMethod string

const (
	MethodGet     HttpMethod = "GET"
	MethodPost    HttpMethod = "POST"
	MethodPut     HttpMethod = "PUT"
	MethodPatch   HttpMethod = "PATCH"
	MethodDelete  HttpMethod = "DELETE"
	MethodOptions HttpMethod = "OPTIONS"
	MethodHead    HttpMethod = "HEAD"
)

type BruHeader struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
}

// Param types: query params live in `params:query`, path params (`:id` URL
// segments) in `params:path`.
const (
	ParamTypeQuery = ""
	ParamTypePath  = "path"
)

type BruParam struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
	Type    string `json:"type,omitempty"` // ParamTypeQuery or ParamTypePath
}

type OAuth2Config struct {
	GrantType       string `json:"grantType"` // "authorization_code", "client_credentials", "password"
	AuthURL         string `json:"authUrl,omitempty"`
	TokenURL        string `json:"tokenUrl"`
	ClientID        string `json:"clientId"`
	ClientSecret    string `json:"clientSecret,omitempty"`
	Username        string `json:"username,omitempty"`
	Password        string `json:"password,omitempty"`
	Scope           string `json:"scope,omitempty"`
	RedirectURI     string `json:"redirectUri,omitempty"`
	RefreshTokenURL string `json:"refreshTokenUrl,omitempty"`
	PKCE            bool   `json:"pkce"`
}

type BrunoRequest struct {
	ID                 string        `json:"id"`
	Name               string        `json:"name"`
	Filename           string        `json:"filename"`
	FilePath           string        `json:"filePath"`
	RelativePath       string        `json:"relativePath"`
	Seq                int           `json:"seq"`
	Method             HttpMethod    `json:"method"`
	URL                string        `json:"url"`
	Headers            []BruHeader   `json:"headers"`
	Params             []BruParam    `json:"params"`
	BodyType           string        `json:"bodyType"` // "none", "json", "text", "xml", "formUrlEncoded"
	BodyContent        string        `json:"bodyContent"`
	AuthType           string        `json:"authType"` // "none", "bearer", "basic", "oauth2", "inherit"
	AuthBearerToken    string        `json:"authBearerToken,omitempty"`
	AuthBasicUser      string        `json:"authBasicUser,omitempty"`
	AuthBasicPassword  string        `json:"authBasicPassword,omitempty"`
	AuthOAuth2         *OAuth2Config `json:"authOAuth2,omitempty"`
	PreRequestScript   string        `json:"preRequestScript,omitempty"`
	PostResponseScript string        `json:"postResponseScript,omitempty"`
	Docs               string        `json:"docs,omitempty"`

	// rawBlocks are the blocks read from disk; blocks brui does not manage are
	// written back verbatim on save.
	rawBlocks []BruBlock
	rawTail   string // extra trailing newlines of the original file
}

// Clone returns a deep copy of the request, so scripts can mutate the copy
// used for execution without touching the request loaded in the editor.
func (r *BrunoRequest) Clone() *BrunoRequest {
	c := *r
	c.Headers = append([]BruHeader(nil), r.Headers...)
	c.Params = append([]BruParam(nil), r.Params...)
	if r.AuthOAuth2 != nil {
		o := *r.AuthOAuth2
		c.AuthOAuth2 = &o
	}
	return &c
}

type BrunoFolder struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Path         string          `json:"path"`
	RelativePath string          `json:"relativePath"`
	Folders      []*BrunoFolder  `json:"folders"`
	Requests     []*BrunoRequest `json:"requests"`
}

type BrunoEnvironment struct {
	Name      string            `json:"name"`
	FilePath  string            `json:"filePath,omitempty"`
	Variables map[string]string `json:"variables"`

	rawBlocks []BruBlock
	rawTail   string
}

type BrunoCollection struct {
	Name                       string              `json:"name"`
	Path                       string              `json:"path"`
	Folders                    []*BrunoFolder      `json:"folders"`
	Requests                   []*BrunoRequest     `json:"requests"`
	Environments               []*BrunoEnvironment `json:"environments"`
	ActiveEnvironment          string              `json:"activeEnvironment,omitempty"`
	RuntimeVariables           map[string]any      `json:"runtimeVariables"`
	CollectionHeaders          []BruHeader         `json:"collectionHeaders"`
	CollectionAuthType         string              `json:"collectionAuthType"` // "none", "bearer", "basic", "oauth2"
	CollectionAuthBearerToken  string              `json:"collectionAuthBearerToken,omitempty"`
	CollectionAuthOAuth2       *OAuth2Config       `json:"collectionAuthOAuth2,omitempty"`
	CollectionPreRequestScript string              `json:"collectionPreRequestScript,omitempty"`

	collectionBlocks []BruBlock
	collectionTail   string
}

type HTTPResponse struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText"`
	DurationMs int64             `json:"durationMs"`
	SizeBytes  int64             `json:"sizeBytes"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	IsJSON     bool              `json:"isJson"`
}

type ConsoleLogEntry struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"` // "info", "warn", "error"
	Message   string `json:"message"`
}
