import * as crypto from 'node:crypto';
import { BrunoCollection } from '../types';

export class Interpolator {
  public static interpolate(
    text: string,
    collection: BrunoCollection,
    extraVars?: Record<string, any>
  ): string {
    if (!text || typeof text !== 'string') return text;

    return text.replace(/\{\{([a-zA-Z0-9_$.,-]+)\}\}/g, (match, varName) => {
      // 1. Built-in dynamic generators
      if (varName === '$uuid' || varName === '$guid') {
        return crypto.randomUUID();
      }
      if (varName === '$timestamp') {
        return Math.floor(Date.now() / 1000).toString();
      }
      if (varName === '$isoTimestamp') {
        return new Date().toISOString();
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

  /**
   * Highlights {{VAR_1}} template placeholders using the theme's variable color (e.g. Catppuccin Violet/Mauve),
   * or missingColor if the variable is not defined in the active environment / runtime.
   */
  public static highlightVariables(
    text: string,
    collection: BrunoCollection,
    varColor: string,
    missingColor?: string
  ): string {
    if (!text || typeof text !== 'string') return text;

    const escaped = text.replace(/\{/g, '\x01').replace(/\}/g, '\x02');
    const highlighted = escaped.replace(/\x01\x01([a-zA-Z0-9_$.,-]+)\x02\x02/g, (_match, varName) => {
      const isDynamic =
        varName === '$uuid' ||
        varName === '$guid' ||
        varName === '$timestamp' ||
        varName === '$isoTimestamp' ||
        varName === '$randomInt';
      const resolved = isDynamic ? '<dynamic>' : Interpolator.getVariable(varName, collection);
      const color = resolved !== undefined ? varColor : (missingColor || varColor);
      return `{bold}{${color}-fg}{open}{open}${varName}{close}{close}{/${color}-fg}{/bold}`;
    });
    return highlighted.replace(/\x01/g, '{open}').replace(/\x02/g, '{close}');
  }

  /**
   * Interpolates {{VAR_1}} and colors the resolved value in varColor (or keeps {{VAR_1}} in missingColor if undefined).
   */
  public static interpolateWithColor(
    text: string,
    collection: BrunoCollection,
    varColor: string,
    missingColor?: string
  ): string {
    if (!text || typeof text !== 'string') return text;

    const escaped = text.replace(/\{/g, '\x01').replace(/\}/g, '\x02');
    const interpolated = escaped.replace(/\x01\x01([a-zA-Z0-9_$.,-]+)\x02\x02/g, (_match, varName) => {
      if (varName === '$uuid' || varName === '$guid') {
        return `{bold}{${varColor}-fg}${crypto.randomUUID()}{/${varColor}-fg}{/bold}`;
      }
      if (varName === '$timestamp') {
        return `{bold}{${varColor}-fg}${Math.floor(Date.now() / 1000)}{/${varColor}-fg}{/bold}`;
      }
      if (varName === '$isoTimestamp') {
        return `{bold}{${varColor}-fg}${new Date().toISOString()}{/${varColor}-fg}{/bold}`;
      }
      if (varName === '$randomInt') {
        return `{bold}{${varColor}-fg}${Math.floor(Math.random() * 1000)}{/${varColor}-fg}{/bold}`;
      }

      const resolved = Interpolator.getVariable(varName, collection);
      if (resolved !== undefined) {
        const safeRes = String(resolved).replace(/\{/g, '{open}').replace(/\}/g, '{close}');
        return `{bold}{${varColor}-fg}${safeRes}{/${varColor}-fg}{/bold}`;
      }

      const errCol = missingColor || varColor;
      return `{bold}{${errCol}-fg}{open}{open}${varName}{close}{close}{/${errCol}-fg}{/bold}`;
    });
    return interpolated.replace(/\x01/g, '{open}').replace(/\x02/g, '{close}');
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
