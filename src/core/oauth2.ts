import * as http from 'node:http';
import * as crypto from 'node:crypto';
import open from 'open';
import { OAuth2Config, BrunoCollection } from '../types';
import { Interpolator } from './interpolation';

export interface OAuth2Result {
  accessToken: string;
  refreshToken?: string;
  expiresIn?: number;
  tokenType?: string;
}

export class OAuth2Handler {
  public static async authenticate(
    config: OAuth2Config,
    collection: BrunoCollection,
    onProgress?: (msg: string) => void
  ): Promise<OAuth2Result> {
    const grantType = config.grantType || 'authorization_code';

    if (grantType === 'client_credentials') {
      return this.handleClientCredentials(config, collection, onProgress);
    } else if (grantType === 'password') {
      return this.handlePassword(config, collection, onProgress);
    } else {
      return this.handleAuthorizationCode(config, collection, onProgress);
    }
  }

  private static async handlePassword(
    config: OAuth2Config,
    collection: BrunoCollection,
    onProgress?: (msg: string) => void
  ): Promise<OAuth2Result> {
    onProgress?.('Requesting token via OAuth2 Password Grant (ROPC)...');
    const tokenUrl = Interpolator.interpolate(config.tokenUrl, collection);
    const clientId = Interpolator.interpolate(config.clientId, collection);
    const clientSecret = config.clientSecret ? Interpolator.interpolate(config.clientSecret, collection) : '';
    const username = config.username ? Interpolator.interpolate(config.username, collection) : '';
    const password = config.password ? Interpolator.interpolate(config.password, collection) : '';

    if (!tokenUrl) throw new Error('OAuth2 Token URL is missing');
    if (!clientId) throw new Error('OAuth2 Client ID is missing');
    if (!username) throw new Error('OAuth2 Username is missing for Password grant');

    const bodyParams = new URLSearchParams();
    bodyParams.append('grant_type', 'password');
    bodyParams.append('client_id', clientId);
    if (clientSecret) bodyParams.append('client_secret', clientSecret);
    bodyParams.append('username', username);
    if (password) bodyParams.append('password', password);
    if (config.scope) bodyParams.append('scope', Interpolator.interpolate(config.scope, collection));

    const resp = await fetch(tokenUrl, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
        'Accept': 'application/json'
      },
      body: bodyParams.toString()
    });

    const data = await resp.json() as any;
    if (!resp.ok) {
      throw new Error(`OAuth2 error (${resp.status}): ${data.error_description || data.error || resp.statusText}`);
    }

    const result: OAuth2Result = {
      accessToken: data.access_token,
      refreshToken: data.refresh_token,
      expiresIn: data.expires_in,
      tokenType: data.token_type
    };

    this.saveTokenToEnvironment(result, collection);
    return result;
  }

  private static async handleClientCredentials(
    config: OAuth2Config,
    collection: BrunoCollection,
    onProgress?: (msg: string) => void
  ): Promise<OAuth2Result> {
    onProgress?.('Requesting token via Client Credentials...');
    const tokenUrl = Interpolator.interpolate(config.tokenUrl, collection);
    const clientId = Interpolator.interpolate(config.clientId, collection);
    const clientSecret = config.clientSecret ? Interpolator.interpolate(config.clientSecret, collection) : '';

    const bodyParams = new URLSearchParams();
    bodyParams.append('grant_type', 'client_credentials');
    bodyParams.append('client_id', clientId);
    if (clientSecret) bodyParams.append('client_secret', clientSecret);
    if (config.scope) bodyParams.append('scope', Interpolator.interpolate(config.scope, collection));

    const resp = await fetch(tokenUrl, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
        'Accept': 'application/json'
      },
      body: bodyParams.toString()
    });

    const data = await resp.json() as any;
    if (!resp.ok) {
      throw new Error(`OAuth2 error: ${data.error_description || data.error || resp.statusText}`);
    }

    const result: OAuth2Result = {
      accessToken: data.access_token,
      refreshToken: data.refresh_token,
      expiresIn: data.expires_in,
      tokenType: data.token_type
    };

    this.saveTokenToEnvironment(result, collection);
    return result;
  }

  private static async handleAuthorizationCode(
    config: OAuth2Config,
    collection: BrunoCollection,
    onProgress?: (msg: string) => void
  ): Promise<OAuth2Result> {
    if (!config.authUrl) throw new Error('OAuth2 Authorization URL is required for Authorization Code grant');
    if (!config.tokenUrl) throw new Error('OAuth2 Token URL is required');
    if (!config.clientId) throw new Error('OAuth2 Client ID is required');

    let port = 3000;
    const redirectUri = config.redirectUri
      ? Interpolator.interpolate(config.redirectUri, collection)
      : `http://127.0.0.1:${port}/callback`;

    try {
      const u = new URL(redirectUri);
      if (u.port) port = parseInt(u.port, 10);
    } catch {
      port = 3000;
    }

    // 1. Generate PKCE if enabled
    let codeVerifier: string | undefined;
    let codeChallenge: string | undefined;

    if (config.pkce !== false) {
      codeVerifier = crypto.randomBytes(32).toString('base64url');
      codeChallenge = crypto.createHash('sha256').update(codeVerifier).digest('base64url');
    }

    const state = crypto.randomBytes(16).toString('hex');

    // 2. Start local loopback server
    return new Promise<OAuth2Result>((resolve, reject) => {
      let server: http.Server;

      const timeout = setTimeout(() => {
        if (server) server.close();
        reject(new Error('OAuth2 authorization timed out after 3 minutes.'));
      }, 180000);

      server = http.createServer(async (req, res) => {
        try {
          const reqUrl = new URL(req.url || '/', `http://127.0.0.1:${port}`);
          if (reqUrl.pathname !== '/callback' && reqUrl.pathname !== '/') {
            res.writeHead(404);
            res.end();
            return;
          }

          const code = reqUrl.searchParams.get('code');
          const error = reqUrl.searchParams.get('error');
          const errorDesc = reqUrl.searchParams.get('error_description');

          if (error) {
            res.writeHead(400, { 'Content-Type': 'text/html' });
            res.end(`<html><body style="font-family:sans-serif;padding:2rem;text-align:center;"><h2>OAuth2 Authentication Failed</h2><p>${errorDesc || error}</p><p>You can close this tab.</p></body></html>`);
            clearTimeout(timeout);
            server.close();
            reject(new Error(`OAuth2 Authorization Failed: ${errorDesc || error}`));
            return;
          }

          if (!code) {
            res.writeHead(400, { 'Content-Type': 'text/html' });
            res.end('<html><body><h3>Missing authorization code</h3></body></html>');
            return;
          }

          // Return success response to browser
          res.writeHead(200, { 'Content-Type': 'text/html' });
          res.end(`
            <html>
              <body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; background: #1e1e2e; color: #cdd6f4; text-align: center; padding: 4rem;">
                <h1 style="color: #a6e3a1;">&#10004; Authenticated Successfully!</h1>
                <p style="font-size: 1.2rem; color: #bac2de;">You can close this window and return to <strong>brui</strong>.</p>
              </body>
            </html>
          `);

          clearTimeout(timeout);
          server.close();

          onProgress?.('Authorization code received! Exchanging for token...');

          // 3. Exchange code for access token
          const tokenUrl = Interpolator.interpolate(config.tokenUrl, collection);
          const clientId = Interpolator.interpolate(config.clientId, collection);
          const clientSecret = config.clientSecret ? Interpolator.interpolate(config.clientSecret, collection) : '';

          const bodyParams = new URLSearchParams();
          bodyParams.append('grant_type', 'authorization_code');
          bodyParams.append('client_id', clientId);
          if (clientSecret) bodyParams.append('client_secret', clientSecret);
          bodyParams.append('code', code);
          bodyParams.append('redirect_uri', redirectUri);
          if (codeVerifier) bodyParams.append('code_verifier', codeVerifier);

          const tokenResp = await fetch(tokenUrl, {
            method: 'POST',
            headers: {
              'Content-Type': 'application/x-www-form-urlencoded',
              'Accept': 'application/json'
            },
            body: bodyParams.toString()
          });

          const tokenData = await tokenResp.json() as any;
          if (!tokenResp.ok) {
            reject(new Error(`Token exchange error: ${tokenData.error_description || tokenData.error || tokenResp.statusText}`));
            return;
          }

          const result: OAuth2Result = {
            accessToken: tokenData.access_token,
            refreshToken: tokenData.refresh_token,
            expiresIn: tokenData.expires_in,
            tokenType: tokenData.token_type
          };

          this.saveTokenToEnvironment(result, collection);
          resolve(result);
        } catch (err: any) {
          clearTimeout(timeout);
          server.close();
          reject(err);
        }
      });

      server.listen(port, async () => {
        onProgress?.(`Loopback server listening on ${redirectUri}`);
        const authUrlBase = Interpolator.interpolate(config.authUrl || '', collection);
        const clientId = Interpolator.interpolate(config.clientId, collection);

        const authParams = new URLSearchParams();
        authParams.append('response_type', 'code');
        authParams.append('client_id', clientId);
        authParams.append('redirect_uri', redirectUri);
        authParams.append('state', state);
        if (config.scope) authParams.append('scope', Interpolator.interpolate(config.scope, collection));
        if (codeChallenge) {
          authParams.append('code_challenge', codeChallenge);
          authParams.append('code_challenge_method', 'S256');
        }

        const fullAuthUrl = `${authUrlBase}${authUrlBase.includes('?') ? '&' : '?'}${authParams.toString()}`;

        onProgress?.('Opening system browser for login...');
        try {
          await open(fullAuthUrl);
        } catch (openErr: any) {
          onProgress?.(`Could not auto-open browser. Please open manually:\n${fullAuthUrl}`);
        }
      });

      server.on('error', (err: any) => {
        clearTimeout(timeout);
        reject(new Error(`Failed to start OAuth2 loopback server on port ${port}: ${err.message}`));
      });
    });
  }

  private static saveTokenToEnvironment(result: OAuth2Result, collection: BrunoCollection) {
    if (collection.activeEnvironment) {
      const env = collection.environments.find(e => e.name === collection.activeEnvironment);
      if (env) {
        env.variables['access_token'] = result.accessToken;
        if (result.refreshToken) {
          env.variables['refresh_token'] = result.refreshToken;
        }
        if (result.expiresIn) {
          const expTime = Date.now() + (result.expiresIn - 30) * 1000;
          env.variables['access_token_exp'] = String(expTime);
        }
      }
    }
    collection.runtimeVariables['access_token'] = result.accessToken;
    if (result.expiresIn) {
      const expTime = Date.now() + (result.expiresIn - 30) * 1000;
      collection.runtimeVariables['access_token_exp'] = String(expTime);
    }
  }
}
