import * as vm from 'node:vm';
import * as path from 'node:path';
import { createRequire } from 'node:module';
import { BrunoRequest, BrunoCollection, HttpResponse, ConsoleLogEntry } from '../types';
import { HttpClient } from './http-client';
import { Interpolator } from './interpolation';

export class ScriptRunner {
  private collection: BrunoCollection;
  private requestMap: Map<string, BrunoRequest>;
  public logs: ConsoleLogEntry[] = [];

  constructor(collection: BrunoCollection, requestMap: Map<string, BrunoRequest>) {
    this.collection = collection;
    this.requestMap = requestMap;
  }

  public clearLogs() {
    this.logs = [];
  }

  public async runPreRequestScripts(request: BrunoRequest): Promise<void> {
    // 1. Run collection-level pre-request script if present
    if (this.collection.collectionPreRequestScript) {
      await this.executeScript(this.collection.collectionPreRequestScript, request, undefined);
    }

    // 2. Run request-level pre-request script
    if (request.preRequestScript) {
      await this.executeScript(request.preRequestScript, request, undefined);
    }
  }

  public async runPostResponseScript(request: BrunoRequest, response: HttpResponse): Promise<void> {
    if (request.postResponseScript) {
      await this.executeScript(request.postResponseScript, request, response);
    }
  }

  private async executeScript(
    code: string,
    request: BrunoRequest,
    response?: HttpResponse
  ): Promise<void> {
    if (!code || !code.trim()) return;

    // Custom require resolving from collection root
    let collectionRequire: NodeRequire;
    try {
      collectionRequire = createRequire(path.join(this.collection.path, 'package.json'));
    } catch {
      collectionRequire = createRequire(process.cwd());
    }

    // Console capture
    const scriptConsole = {
      log: (...args: any[]) => {
        this.logs.push({
          type: 'log',
          message: args.map(a => typeof a === 'object' ? JSON.stringify(a) : String(a)).join(' '),
          timestamp: new Date()
        });
      },
      info: (...args: any[]) => {
        this.logs.push({
          type: 'info',
          message: args.map(a => typeof a === 'object' ? JSON.stringify(a) : String(a)).join(' '),
          timestamp: new Date()
        });
      },
      warn: (...args: any[]) => {
        this.logs.push({
          type: 'warn',
          message: args.map(a => typeof a === 'object' ? JSON.stringify(a) : String(a)).join(' '),
          timestamp: new Date()
        });
      },
      error: (...args: any[]) => {
        this.logs.push({
          type: 'error',
          message: args.map(a => typeof a === 'object' ? JSON.stringify(a) : String(a)).join(' '),
          timestamp: new Date()
        });
      }
    };

    // The 'bru' object
    const bru = {
      getEnvVar: (name: string): string | undefined => {
        if (!name) return undefined;
        if (this.collection.activeEnvironment) {
          const env = this.collection.environments.find(e => e.name === this.collection.activeEnvironment);
          if (env && env.variables[name] !== undefined) {
            return env.variables[name];
          }
        }
        return process.env[name];
      },
      setEnvVar: (name: string, value: any): void => {
        if (!name) return;
        const strVal = String(value);
        if (this.collection.activeEnvironment) {
          const env = this.collection.environments.find(e => e.name === this.collection.activeEnvironment);
          if (env) {
            env.variables[name] = strVal;
          }
        }
        // Also keep in runtime variables
        this.collection.runtimeVariables[name] = strVal;
      },
      getVar: (name: string): any => {
        return this.collection.runtimeVariables[name];
      },
      setVar: (name: string, value: any): void => {
        this.collection.runtimeVariables[name] = value;
      },
      runRequest: async (targetPath: string): Promise<any> => {
        scriptConsole.info(`[bru.runRequest] Invoking sub-request: "${targetPath}"`);

        // Look up request
        let target = this.requestMap.get(targetPath);
        if (!target) {
          // Try without or with .bru
          target = this.requestMap.get(targetPath.replace(/\.bru$/, '')) ||
                   this.requestMap.get(`${targetPath}.bru`);
        }
        if (!target) {
          // Case-insensitive or basename match
          for (const [k, v] of this.requestMap.entries()) {
            if (k.toLowerCase() === targetPath.toLowerCase() || v.name.toLowerCase() === targetPath.toLowerCase()) {
              target = v;
              break;
            }
          }
        }

        if (!target) {
          const errMsg = `[bru.runRequest] Request not found: "${targetPath}"`;
          scriptConsole.error(errMsg);
          throw new Error(errMsg);
        }

        // Recursively run pre-request scripts for the sub-request
        await this.runPreRequestScripts(target);

        // Execute the sub-request HTTP call
        const subResp = await HttpClient.execute(target, this.collection);
        scriptConsole.info(`[bru.runRequest] "${targetPath}" completed with status ${subResp.status}`);

        // Run post-response script for the sub-request
        await this.runPostResponseScript(target, subResp);

        return {
          status: subResp.status,
          headers: subResp.headers,
          body: subResp.jsonBody || subResp.body
        };
      }
    };

    // The 'req' object
    const req = {
      getUrl: (): string => {
        return Interpolator.interpolate(request.url, this.collection);
      },
      setUrl: (newUrl: string): void => {
        request.url = newUrl;
      },
      getMethod: (): string => request.method,
      setMethod: (newMethod: any): void => {
        request.method = newMethod.toUpperCase();
      },
      getHeader: (name: string): string | undefined => {
        const found = request.headers.find(h => h.name.toLowerCase() === name.toLowerCase());
        return found ? Interpolator.interpolate(found.value, this.collection) : undefined;
      },
      setHeader: (name: string, val: string): void => {
        const found = request.headers.find(h => h.name.toLowerCase() === name.toLowerCase());
        if (found) {
          found.value = String(val);
          found.enabled = true;
        } else {
          request.headers.push({ name, value: String(val), enabled: true });
        }
      },
      getBody: (): any => {
        return request.bodyContent;
      },
      setBody: (b: any): void => {
        request.bodyContent = typeof b === 'object' ? JSON.stringify(b) : String(b);
      }
    };

    // The 'res' object (for post-response)
    const res = response ? {
      getStatus: () => response.status,
      status: response.status,
      getBody: () => response.jsonBody || response.body,
      body: response.jsonBody || response.body,
      getHeader: (name: string) => response.headers[name.toLowerCase()],
      getHeaders: () => response.headers,
      headers: response.headers
    } : undefined;

    // Test runner helpers
    const test = (title: string, fn: () => void) => {
      try {
        fn();
        scriptConsole.info(`✔ Test passed: ${title}`);
      } catch (err: any) {
        scriptConsole.error(`✖ Test failed: ${title} - ${err.message}`);
      }
    };

    const expect = (actual: any) => ({
      to: {
        equal: (expected: any) => {
          if (actual !== expected) throw new Error(`Expected ${expected} but got ${actual}`);
        },
        be: {
          equal: (expected: any) => {
            if (actual !== expected) throw new Error(`Expected ${expected} but got ${actual}`);
          },
          ok: () => {
            if (!actual) throw new Error(`Expected truthy value but got ${actual}`);
          }
        },
        have: {
          property: (prop: string, val?: any) => {
            if (actual == null || !(prop in actual)) {
              throw new Error(`Expected object to have property "${prop}"`);
            }
            if (val !== undefined && actual[prop] !== val) {
              throw new Error(`Expected property "${prop}" to equal ${val} but got ${actual[prop]}`);
            }
          }
        }
      }
    });

    // Create execution context
    const sandbox: Record<string, any> = {
      bru,
      req,
      res,
      test,
      expect,
      console: scriptConsole,
      require: collectionRequire,
      Date,
      Number,
      String,
      Boolean,
      Array,
      Object,
      JSON,
      Math,
      RegExp,
      Buffer,
      setTimeout,
      clearTimeout
    };

    const context = vm.createContext(sandbox);

    // Wrap in async IIFE so top-level await works
    const wrappedCode = `(async () => {
${code}
})()`;

    try {
      const script = new vm.Script(wrappedCode, {
        filename: `${request.name}.js`
      });
      await script.runInContext(context);
    } catch (err: any) {
      scriptConsole.error(`Script error: ${err.message || String(err)}`);
    }
  }
}
