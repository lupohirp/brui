import * as crypto from 'node:crypto';
import { BrunoCollection } from '../types';

export class Interpolator {
  public static interpolate(
    text: string,
    collection: BrunoCollection,
    extraVars?: Record<string, any>
  ): string {
    if (!text || typeof text !== 'string') return text;

    return text.replace(/\{\{([a-zA-Z0-9_.-]+)\}\}/g, (match, varName) => {
      // 1. Built-in dynamic generators
      if (varName === '$uuid' || varName === '$guid') {
        return crypto.randomUUID();
      }
      if (varName === '$timestamp') {
        return Math.floor(Date.now() / 1000).toString();
      }
      if (varName === '$randomInt') {
        return Math.floor(Math.random() * 1000).toString();
      }

      // 2. Extra variables passed per-run
      if (extraVars && Object.prototype.hasOwnProperty.call(extraVars, varName)) {
        return String(extraVars[varName]);
      }

      // 3. Runtime variables (set by bru.setVar)
      if (Object.prototype.hasOwnProperty.call(collection.runtimeVariables, varName)) {
        return String(collection.runtimeVariables[varName]);
      }

      // 4. Active environment variables (set by bru.setEnvVar or env file)
      if (collection.activeEnvironment) {
        const env = collection.environments.find(e => e.name === collection.activeEnvironment);
        if (env && Object.prototype.hasOwnProperty.call(env.variables, varName)) {
          return String(env.variables[varName]);
        }
      }

      // 5. System environment variables
      if (process.env[varName] !== undefined) {
        return process.env[varName]!;
      }

      // If not found, leave match as is
      return match;
    });
  }

  public static getVariable(
    varName: string,
    collection: BrunoCollection,
    extraVars?: Record<string, any>
  ): string | undefined {
    if (extraVars && Object.prototype.hasOwnProperty.call(extraVars, varName)) {
      return String(extraVars[varName]);
    }
    if (Object.prototype.hasOwnProperty.call(collection.runtimeVariables, varName)) {
      return String(collection.runtimeVariables[varName]);
    }
    if (collection.activeEnvironment) {
      const env = collection.environments.find(e => e.name === collection.activeEnvironment);
      if (env && Object.prototype.hasOwnProperty.call(env.variables, varName)) {
        return String(env.variables[varName]);
      }
    }
    return process.env[varName];
  }
}
