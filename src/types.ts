export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'OPTIONS' | 'HEAD';

export interface ThemeColors {
  name: string;
  isDark: boolean;
  bg: string;
  fg: string;
  surface: string;
  surfaceHover: string;
  border: string;
  borderFocus: string;
  selection: string;
  selectionFg: string;
  accent: string;
  subtext: string;
  // HTTP Method badges
  get: string;
  post: string;
  put: string;
  patch: string;
  delete: string;
  options: string;
  head: string;
  // Status colors
  statusSuccess: string;
  statusWarn: string;
  statusError: string;
  statusInfo: string;
}

export interface BruHeader {
  name: string;
  value: string;
  enabled: boolean;
}

export interface BruParam {
  name: string;
  value: string;
  enabled: boolean;
}

export interface OAuth2Config {
  grantType: 'authorization_code' | 'client_credentials' | 'password';
  authUrl?: string;
  tokenUrl: string;
  clientId: string;
  clientSecret?: string;
  username?: string;
  password?: string;
  scope?: string;
  redirectUri?: string;
  refreshTokenUrl?: string;
  pkce?: boolean;
}

export interface BrunoRequest {
  id: string;
  name: string;
  filename: string;
  filePath: string;
  relativePath: string;
  seq: number;
  method: HttpMethod;
  url: string;
  headers: BruHeader[];
  params: BruParam[];
  bodyType: 'none' | 'json' | 'text' | 'xml' | 'formUrlEncoded' | 'multipartForm';
  bodyContent: string;
  authType: 'none' | 'bearer' | 'basic' | 'apikey' | 'oauth2' | 'inherit';
  authBearerToken?: string;
  authBasicUser?: string;
  authBasicPassword?: string;
  authOAuth2?: OAuth2Config;
  preRequestScript?: string;
  postResponseScript?: string;
  docs?: string;
}

export interface BrunoFolder {
  id: string;
  name: string;
  path: string;
  relativePath: string;
  folders: BrunoFolder[];
  requests: BrunoRequest[];
}

export interface BrunoEnvironment {
  name: string;
  filePath?: string;
  variables: Record<string, string>;
}

export interface BrunoCollection {
  name: string;
  path: string;
  folders: BrunoFolder[];
  requests: BrunoRequest[];
  environments: BrunoEnvironment[];
  activeEnvironment?: string;
  runtimeVariables: Record<string, any>;
  collectionHeaders: BruHeader[];
  collectionAuthType: 'none' | 'bearer' | 'basic' | 'oauth2';
  collectionAuthBearerToken?: string;
  collectionAuthOAuth2?: OAuth2Config;
  collectionPreRequestScript?: string;
}

export interface HttpResponse {
  status: number;
  statusText: string;
  durationMs: number;
  sizeBytes: number;
  headers: Record<string, string>;
  body: string;
  jsonBody?: any;
}

export interface ConsoleLogEntry {
  type: 'log' | 'info' | 'warn' | 'error';
  message: string;
  timestamp: Date;
}
