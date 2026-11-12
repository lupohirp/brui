import * as fs from 'node:fs';
import * as path from 'node:path';
import {
  bruToJsonV2,
  collectionBruToJson,
  bruToEnvJsonV2
} from '@usebruno/lang';
import {
  BrunoCollection,
  BrunoFolder,
  BrunoRequest,
  BrunoEnvironment,
  BruHeader,
  BruParam,
  HttpMethod,
  OAuth2Config
} from '../types';

export class CollectionParser {
  private collectionRoot: string;
  public requestMap: Map<string, BrunoRequest> = new Map();

  constructor(collectionPath: string) {
    this.collectionRoot = path.resolve(collectionPath);
  }

  public parse(): BrunoCollection {
    if (!fs.existsSync(this.collectionRoot)) {
      throw new Error(`Collection path does not exist: ${this.collectionRoot}`);
    }

    let collectionName = path.basename(this.collectionRoot);
    let collectionHeaders: BruHeader[] = [];
    let collectionAuthType: 'none' | 'bearer' | 'basic' = 'none';
    let collectionAuthBearerToken: string | undefined;
    let collectionPreRequestScript: string | undefined;

    // Check for bruno.json
    const brunoJsonPath = path.join(this.collectionRoot, 'bruno.json');
    if (fs.existsSync(brunoJsonPath)) {
      try {
        const raw = JSON.parse(fs.readFileSync(brunoJsonPath, 'utf8'));
        if (raw.name) collectionName = raw.name;
      } catch (err) {
        // ignore malformed bruno.json
      }
    }

    // Check for collection.bru
    const collectionBruPath = path.join(this.collectionRoot, 'collection.bru');
    if (fs.existsSync(collectionBruPath)) {
      try {
        const content = fs.readFileSync(collectionBruPath, 'utf8');
        const parsed = collectionBruToJson(content) as any;
        if (parsed.meta?.name) collectionName = parsed.meta.name;
        if (parsed.headers && Array.isArray(parsed.headers)) {
          collectionHeaders = parsed.headers.map((h: any) => ({
            name: h.name || '',
            value: h.value || '',
            enabled: h.enabled !== false
          }));
        }
        if (parsed.auth?.mode) {
          collectionAuthType = parsed.auth.mode;
          if (parsed.auth.mode === 'bearer') {
            collectionAuthBearerToken = parsed.auth.bearer?.token;
          }
        }
        if (parsed.script?.req) {
          collectionPreRequestScript = parsed.script.req;
        }
      } catch (err) {
        // ignore malformed collection.bru
      }
    }

    // Parse environments
    const environments: BrunoEnvironment[] = [];
    const envDir = path.join(this.collectionRoot, 'environments');
    if (fs.existsSync(envDir) && fs.statSync(envDir).isDirectory()) {
      const files = fs.readdirSync(envDir);
      for (const file of files) {
        if (file.endsWith('.bru')) {
          const filePath = path.join(envDir, file);
          const envName = file.slice(0, -4);
          try {
            const content = fs.readFileSync(filePath, 'utf8');
            const parsed = bruToEnvJsonV2(content) as any;
            const variables: Record<string, string> = {};
            if (parsed.variables && Array.isArray(parsed.variables)) {
              for (const v of parsed.variables) {
                if (v.enabled !== false && v.name) {
                  variables[v.name] = v.value || '';
                }
              }
            }
            environments.push({
              name: envName,
              filePath,
              variables
            });
          } catch (e) {
            // ignore malformed env file
          }
        }
      }
    }

    // Recursively parse requests and folders
    this.requestMap.clear();
    const { folders, requests } = this.scanDirectory(this.collectionRoot, '');

    return {
      name: collectionName,
      path: this.collectionRoot,
      folders,
      requests,
      environments,
      activeEnvironment: environments.length > 0 ? environments[0].name : undefined,
      runtimeVariables: {},
      collectionHeaders,
      collectionAuthType,
      collectionAuthBearerToken,
      collectionPreRequestScript
    };
  }

  private scanDirectory(dir: string, relPath: string): { folders: BrunoFolder[]; requests: BrunoRequest[] } {
    const folders: BrunoFolder[] = [];
    const requests: BrunoRequest[] = [];

    const entries = fs.readdirSync(dir, { withFileTypes: true });

    // Filter and sort entries
    for (const entry of entries) {
      if (entry.name.startsWith('.') || entry.name === 'node_modules') {
        continue;
      }

      const fullPath = path.join(dir, entry.name);
      const currentRelPath = relPath ? `${relPath}/${entry.name}` : entry.name;

      if (entry.isDirectory()) {
        if (entry.name === 'environments') {
          continue; // Environments handled separately
        }
        const sub = this.scanDirectory(fullPath, currentRelPath);
        folders.push({
          id: currentRelPath,
          name: entry.name,
          path: fullPath,
          relativePath: currentRelPath,
          folders: sub.folders,
          requests: sub.requests
        });
      } else if (entry.isFile() && entry.name.endsWith('.bru')) {
        if (entry.name === 'collection.bru' || entry.name === 'folder.bru') {
          continue;
        }

        const req = this.parseRequestFile(fullPath, currentRelPath);
        if (req) {
          requests.push(req);
          // Register in map by relative path without extension & with extension
          const cleanRel = currentRelPath.slice(0, -4);
          this.requestMap.set(cleanRel, req);
          this.requestMap.set(currentRelPath, req);
          this.requestMap.set(req.name, req);
        }
      }
    }

    // Sort requests by sequence number or name
    requests.sort((a, b) => a.seq - b.seq || a.name.localeCompare(b.name));
    folders.sort((a, b) => a.name.localeCompare(b.name));

    return { folders, requests };
  }

  private parseRequestFile(filePath: string, relativePath: string): BrunoRequest | null {
    try {
      const content = fs.readFileSync(filePath, 'utf8');
      const parsed = bruToJsonV2(content) as any;

      if (!parsed || !parsed.http) {
        return null;
      }

      const method = (parsed.http.method || 'GET').toUpperCase() as HttpMethod;
      const url = parsed.http.url || '';
      const name = parsed.meta?.name || path.basename(filePath, '.bru');
      const seq = Number(parsed.meta?.seq) || 999;

      // Headers
      const headers: BruHeader[] = [];
      if (parsed.headers && Array.isArray(parsed.headers)) {
        for (const h of parsed.headers) {
          headers.push({
            name: h.name || '',
            value: h.value || '',
            enabled: h.enabled !== false
          });
        }
      }

      // Query Params
      const params: BruParam[] = [];
      if (parsed.params && Array.isArray(parsed.params)) {
        for (const p of parsed.params) {
          params.push({
            name: p.name || '',
            value: p.value || '',
            enabled: p.enabled !== false
          });
        }
      }

      // Body
      let bodyType: BrunoRequest['bodyType'] = 'none';
      let bodyContent = '';

      if (parsed.http.body) {
        bodyType = parsed.http.body;
        if (bodyType === 'json' && parsed.body?.json) {
          bodyContent = typeof parsed.body.json === 'string'
            ? parsed.body.json
            : JSON.stringify(parsed.body.json, null, 2);
        } else if (bodyType === 'text' && parsed.body?.text) {
          bodyContent = parsed.body.text;
        } else if (bodyType === 'xml' && parsed.body?.xml) {
          bodyContent = parsed.body.xml;
        } else if (bodyType === 'formUrlEncoded' && parsed.body?.formUrlEncoded) {
          if (Array.isArray(parsed.body.formUrlEncoded)) {
            const kv = parsed.body.formUrlEncoded
              .filter((x: any) => x.enabled !== false && x.name)
              .map((x: any) => `${encodeURIComponent(x.name)}=${encodeURIComponent(x.value || '')}`)
              .join('&');
            bodyContent = kv;
          } else if (typeof parsed.body.formUrlEncoded === 'string') {
            bodyContent = parsed.body.formUrlEncoded;
          }
        }
      }

      // Auth
      let authType: BrunoRequest['authType'] = 'none';
      let authBearerToken: string | undefined;
      let authBasicUser: string | undefined;
      let authBasicPassword: string | undefined;
      let authOAuth2: OAuth2Config | undefined;

      if (parsed.http.auth) {
        authType = parsed.http.auth;
      } else if (parsed.auth?.mode) {
        authType = parsed.auth.mode;
      }

      if (parsed.auth) {
        if (parsed.auth.bearer?.token) {
          authBearerToken = parsed.auth.bearer.token;
        }
        if (parsed.auth.basic) {
          authBasicUser = parsed.auth.basic.username;
          authBasicPassword = parsed.auth.basic.password;
        }
        if (parsed.auth.oauth2) {
          const o = parsed.auth.oauth2;
          authOAuth2 = {
            grantType: o.grantType || 'authorization_code',
            authUrl: o.authUrl || o.authorizationUrl,
            tokenUrl: o.tokenUrl || o.accessTokenUrl,
            clientId: o.clientId,
            clientSecret: o.clientSecret,
            scope: o.scope,
            redirectUri: o.redirectUri || o.callbackUrl,
            pkce: o.pkce !== false
          };
        }
      }

      return {
        id: relativePath,
        name,
        filename: path.basename(filePath),
        filePath,
        relativePath,
        seq,
        method,
        url,
        headers,
        params,
        bodyType,
        bodyContent,
        authType,
        authBearerToken,
        authBasicUser,
        authBasicPassword,
        authOAuth2,
        preRequestScript: parsed.script?.req,
        postResponseScript: parsed.script?.res,
        docs: parsed.docs
      };
    } catch (err) {
      return null;
    }
  }
}
