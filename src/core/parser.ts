import * as fs from 'node:fs';
import * as path from 'node:path';
import {
  bruToJsonV2,
  collectionBruToJson,
  bruToEnvJsonV2
} from '@usebruno/lang';
import { BrunoSecretsManager } from './secrets';
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

export interface DiscoveredCollection {
  name: string;
  path: string;
}

export class CollectionParser {
  private collectionRoot: string;
  public requestMap: Map<string, BrunoRequest> = new Map();

  constructor(collectionPath: string) {
    this.collectionRoot = path.resolve(collectionPath);
  }

  public static isBrunoCollection(dir: string): boolean {
    if (!fs.existsSync(dir) || !fs.statSync(dir).isDirectory()) return false;
    return (
      fs.existsSync(path.join(dir, 'bruno.json')) ||
      fs.existsSync(path.join(dir, 'collection.bru'))
    );
  }

  public static discoverCollections(baseDir: string): DiscoveredCollection[] {
    const results: DiscoveredCollection[] = [];
    const resolved = path.resolve(baseDir);

    if (!fs.existsSync(resolved) || !fs.statSync(resolved).isDirectory()) {
      return results;
    }

    // Check if the directory itself is a collection
    if (this.isBrunoCollection(resolved)) {
      results.push({
        name: this.getCollectionName(resolved),
        path: resolved
      });
    }

    // Check immediate subdirectories
    try {
      const entries = fs.readdirSync(resolved, { withFileTypes: true });
      for (const entry of entries) {
        if (entry.isDirectory() && !entry.name.startsWith('.') && entry.name !== 'node_modules') {
          const subPath = path.join(resolved, entry.name);
          if (this.isBrunoCollection(subPath)) {
            // Avoid duplicates
            if (!results.some(r => r.path === subPath)) {
              results.push({
                name: this.getCollectionName(subPath),
                path: subPath
              });
            }
          }
        }
      }
    } catch {
      // ignore read errors
    }

    // Also check parent directory for sibling collections!
    try {
      const parentDir = path.dirname(resolved);
      if (parentDir && parentDir !== resolved) {
        const parentEntries = fs.readdirSync(parentDir, { withFileTypes: true });
        for (const entry of parentEntries) {
          if (entry.isDirectory() && !entry.name.startsWith('.') && entry.name !== 'node_modules') {
            const siblingPath = path.join(parentDir, entry.name);
            if (this.isBrunoCollection(siblingPath) && !results.some(r => r.path === siblingPath)) {
              results.push({
                name: this.getCollectionName(siblingPath),
                path: siblingPath
              });
            }
          }
        }
      }
    } catch {
      // ignore
    }

    return results;
  }

  private static getCollectionName(dir: string): string {
    const brunoJsonPath = path.join(dir, 'bruno.json');
    if (fs.existsSync(brunoJsonPath)) {
      try {
        const raw = JSON.parse(fs.readFileSync(brunoJsonPath, 'utf8'));
        if (raw.name) return raw.name;
      } catch {}
    }
    const collBru = path.join(dir, 'collection.bru');
    if (fs.existsSync(collBru)) {
      try {
        const parsed = collectionBruToJson(fs.readFileSync(collBru, 'utf8')) as any;
        if (parsed.meta?.name) return parsed.meta.name;
      } catch {}
    }
    return path.basename(dir);
  }

  public parse(): BrunoCollection {
    if (!fs.existsSync(this.collectionRoot)) {
      throw new Error(`Collection path does not exist: ${this.collectionRoot}`);
    }

    let collectionName = CollectionParser.getCollectionName(this.collectionRoot);
    let collectionHeaders: BruHeader[] = [];
    let collectionAuthType: 'none' | 'bearer' | 'basic' | 'oauth2' = 'none';
    let collectionAuthBearerToken: string | undefined;
    let collectionAuthOAuth2: OAuth2Config | undefined;
    let collectionPreRequestScript: string | undefined;

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
          } else if (parsed.auth.mode === 'oauth2' && parsed.auth.oauth2) {
            const o = parsed.auth.oauth2;
            collectionAuthOAuth2 = {
              grantType: o.grantType || 'authorization_code',
              authUrl: o.authUrl || o.authorizationUrl,
              tokenUrl: o.tokenUrl || o.accessTokenUrl,
              clientId: o.clientId,
              clientSecret: o.clientSecret,
              username: o.username,
              password: o.password,
              scope: o.scope,
              redirectUri: o.redirectUri || o.callbackUrl,
              refreshTokenUrl: o.refreshTokenUrl,
              pkce: o.pkce !== false
            };
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

    // Merge decrypted secrets from ~/.config/bruno/secrets.json
    const decryptedSecrets = BrunoSecretsManager.loadCollectionSecrets(this.collectionRoot);
    for (const env of environments) {
      if (decryptedSecrets[env.name]) {
        for (const [secKey, secVal] of Object.entries(decryptedSecrets[env.name])) {
          if (secVal) {
            env.variables[secKey] = secVal;
          }
        }
      }
    }

    // Merge .env file if present at collection root
    const dotEnvPath = path.join(this.collectionRoot, '.env');
    if (fs.existsSync(dotEnvPath)) {
      try {
        const lines = fs.readFileSync(dotEnvPath, 'utf8').split('\n');
        for (const line of lines) {
          const trimmed = line.trim();
          if (trimmed && !trimmed.startsWith('#') && trimmed.includes('=')) {
            const eqIdx = trimmed.indexOf('=');
            const k = trimmed.slice(0, eqIdx).trim();
            const v = trimmed.slice(eqIdx + 1).trim();
            for (const env of environments) {
              if (!env.variables[k]) {
                env.variables[k] = v;
              }
            }
          }
        }
      } catch {}
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
      collectionAuthOAuth2,
      collectionPreRequestScript
    };
  }

  private scanDirectory(dir: string, relPath: string): { folders: BrunoFolder[]; requests: BrunoRequest[] } {
    const folders: BrunoFolder[] = [];
    const requests: BrunoRequest[] = [];

    const entries = fs.readdirSync(dir, { withFileTypes: true });

    for (const entry of entries) {
      if (entry.name.startsWith('.') || entry.name === 'node_modules') {
        continue;
      }

      const fullPath = path.join(dir, entry.name);
      const currentRelPath = relPath ? `${relPath}/${entry.name}` : entry.name;

      if (entry.isDirectory()) {
        if (entry.name === 'environments') {
          continue;
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
          const cleanRel = currentRelPath.slice(0, -4);
          this.requestMap.set(cleanRel, req);
          this.requestMap.set(currentRelPath, req);
          this.requestMap.set(req.name, req);
        }
      }
    }

    requests.sort((a, b) => a.seq - b.seq || a.name.localeCompare(b.name));
    folders.sort((a, b) => a.name.localeCompare(b.name));

    return { folders, requests };
  }

  public parseRequestFile(filePath: string, relativePath: string): BrunoRequest | null {
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
              .map((x: any) => `${x.name}=${x.value || ''}`)
              .join('\n');
            bodyContent = kv;
          } else if (typeof parsed.body.formUrlEncoded === 'string') {
            bodyContent = parsed.body.formUrlEncoded;
          }
        }
      }

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
            username: o.username,
            password: o.password,
            scope: o.scope,
            redirectUri: o.redirectUri || o.callbackUrl,
            refreshTokenUrl: o.refreshTokenUrl,
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
