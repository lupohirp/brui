import { BrunoRequest, BrunoCollection, HttpResponse, HttpMethod } from '../types';
import { Interpolator } from './interpolation';

export class HttpClient {
  public static async execute(
    request: BrunoRequest,
    collection: BrunoCollection
  ): Promise<HttpResponse> {
    // 1. Interpolate URL
    let targetUrl = Interpolator.interpolate(request.url, collection);

    // 2. Append query params
    const activeParams = request.params.filter(p => p.enabled && p.name);
    if (activeParams.length > 0) {
      try {
        const parsedUrl = new URL(targetUrl);
        for (const p of activeParams) {
          const key = Interpolator.interpolate(p.name, collection);
          const val = Interpolator.interpolate(p.value, collection);
          parsedUrl.searchParams.append(key, val);
        }
        targetUrl = parsedUrl.toString();
      } catch (err) {
        // If URL is relative or invalid, manual append
        const queryStr = activeParams
          .map(p => `${encodeURIComponent(Interpolator.interpolate(p.name, collection))}=${encodeURIComponent(Interpolator.interpolate(p.value, collection))}`)
          .join('&');
        targetUrl += (targetUrl.includes('?') ? '&' : '?') + queryStr;
      }
    }

    // 3. Prepare headers (Collection headers first, request headers override)
    const headersMap: Record<string, string> = {};

    for (const ch of collection.collectionHeaders) {
      if (ch.enabled && ch.name) {
        headersMap[ch.name.toLowerCase()] = Interpolator.interpolate(ch.value, collection);
      }
    }

    for (const rh of request.headers) {
      if (rh.enabled && rh.name) {
        headersMap[rh.name.toLowerCase()] = Interpolator.interpolate(rh.value, collection);
      }
    }

    // 4. Resolve Auth
    let effectiveAuth = request.authType;
    if (effectiveAuth === 'inherit') {
      effectiveAuth = collection.collectionAuthType as any;
    }

    if (effectiveAuth === 'bearer') {
      const token = request.authBearerToken || collection.collectionAuthBearerToken;
      if (token) {
        const resolvedToken = Interpolator.interpolate(token, collection);
        headersMap['authorization'] = `Bearer ${resolvedToken}`;
      }
    } else if (effectiveAuth === 'basic') {
      const user = request.authBasicUser ? Interpolator.interpolate(request.authBasicUser, collection) : '';
      const pass = request.authBasicPassword ? Interpolator.interpolate(request.authBasicPassword, collection) : '';
      const b64 = Buffer.from(`${user}:${pass}`).toString('base64');
      headersMap['authorization'] = `Basic ${b64}`;
    }

    // 5. Prepare Body
    let body: BodyInit | undefined = undefined;
    if (['POST', 'PUT', 'PATCH', 'DELETE'].includes(request.method)) {
      if (request.bodyType === 'json' && request.bodyContent) {
        body = Interpolator.interpolate(request.bodyContent, collection);
        if (!headersMap['content-type']) {
          headersMap['content-type'] = 'application/json';
        }
      } else if (request.bodyType === 'formUrlEncoded' && request.bodyContent) {
        body = Interpolator.interpolate(request.bodyContent, collection);
        if (!headersMap['content-type']) {
          headersMap['content-type'] = 'application/x-www-form-urlencoded';
        }
      } else if (request.bodyType === 'text' && request.bodyContent) {
        body = Interpolator.interpolate(request.bodyContent, collection);
        if (!headersMap['content-type']) {
          headersMap['content-type'] = 'text/plain';
        }
      } else if (request.bodyType === 'xml' && request.bodyContent) {
        body = Interpolator.interpolate(request.bodyContent, collection);
        if (!headersMap['content-type']) {
          headersMap['content-type'] = 'application/xml';
        }
      }
    }

    // 6. Execute with timing
    const startTime = performance.now();
    try {
      const resp = await fetch(targetUrl, {
        method: request.method,
        headers: headersMap,
        body,
        redirect: 'follow',
      });

      const durationMs = Math.round(performance.now() - startTime);
      const textBody = await resp.text();
      const sizeBytes = Buffer.byteLength(textBody, 'utf8');

      // Extract response headers
      const resHeaders: Record<string, string> = {};
      resp.headers.forEach((val, key) => {
        resHeaders[key] = val;
      });

      // Try JSON parsing
      let jsonBody: any = undefined;
      try {
        jsonBody = JSON.parse(textBody);
      } catch {
        // Not JSON
      }

      return {
        status: resp.status,
        statusText: resp.statusText,
        durationMs,
        sizeBytes,
        headers: resHeaders,
        body: textBody,
        jsonBody
      };
    } catch (err: any) {
      const durationMs = Math.round(performance.now() - startTime);
      return {
        status: 0,
        statusText: err.message || 'Network Error',
        durationMs,
        sizeBytes: 0,
        headers: {},
        body: `Error: ${err.message || String(err)}`,
        jsonBody: undefined
      };
    }
  }
}
