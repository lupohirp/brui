import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';
import * as crypto from 'node:crypto';
import { machineIdSync } from '@usebruno/node-machine-id';

export class BrunoSecretsManager {
  private static cachedMachineId?: string;

  private static getMachineId(): string {
    if (!this.cachedMachineId) {
      try {
        this.cachedMachineId = machineIdSync();
      } catch {
        // Fallback to /etc/machine-id
        try {
          this.cachedMachineId = fs.readFileSync('/etc/machine-id', 'utf8').trim();
        } catch {
          this.cachedMachineId = os.hostname();
        }
      }
    }
    return this.cachedMachineId;
  }

  public static decryptSecret(encryptedVal: string): string {
    if (!encryptedVal || typeof encryptedVal !== 'string') return encryptedVal;

    // Bruno formats: $01:<hex> (AES-256-CBC) or $00:<hex> (safeStorage)
    if (!encryptedVal.startsWith('$01:')) {
      // If it's plain text or not $01, return as is
      return encryptedVal;
    }

    const hexData = encryptedVal.slice(4);
    if (!hexData) return '';

    const rawKey = this.getMachineId();
    const iv = Buffer.alloc(16, 0);
    const key = crypto.createHash('sha256').update(rawKey).digest();

    try {
      const decipher = crypto.createDecipheriv('aes-256-cbc', key, iv);
      let decrypted = decipher.update(hexData, 'hex', 'utf8');
      decrypted += decipher.final('utf8');
      return decrypted;
    } catch (err) {
      // Fallback: older MD5 key derivation
      try {
        const keyOld = Buffer.alloc(32);
        const ivOld = Buffer.alloc(16);
        const derivedBytes: Buffer[] = [];
        let lastHash: Buffer | null = null;
        while (Buffer.concat(derivedBytes).length < 32 + 16) {
          const hash = crypto.createHash('md5');
          if (lastHash) hash.update(lastHash);
          hash.update(Buffer.from(rawKey, 'utf8'));
          lastHash = hash.digest();
          derivedBytes.push(lastHash);
        }
        const concat = Buffer.concat(derivedBytes);
        concat.copy(keyOld, 0, 0, 32);
        concat.copy(ivOld, 0, 32, 48);
        const decipher2 = crypto.createDecipheriv('aes-256-cbc', keyOld, ivOld);
        let dec2 = decipher2.update(hexData, 'hex', 'utf8');
        dec2 += decipher2.final('utf8');
        return dec2;
      } catch {
        return '';
      }
    }
  }

  public static loadCollectionSecrets(collectionPath: string): Record<string, Record<string, string>> {
    // Returns: { [envName]: { [secretName]: decryptedValue } }
    const results: Record<string, Record<string, string>> = {};

    const secretsPath = path.join(os.homedir(), '.config/bruno/secrets.json');
    if (!fs.existsSync(secretsPath)) {
      return results;
    }

    try {
      const raw = JSON.parse(fs.readFileSync(secretsPath, 'utf8'));
      if (!raw || !Array.isArray(raw.collections)) return results;

      const normTarget = path.resolve(collectionPath).toLowerCase();

      for (const col of raw.collections) {
        if (!col.path) continue;
        const normCol = path.resolve(col.path).toLowerCase();

        // Match collection by path or folder basename
        if (normCol === normTarget || path.basename(normCol) === path.basename(normTarget)) {
          if (Array.isArray(col.environments)) {
            for (const env of col.environments) {
              const envName = env.name;
              if (!envName) continue;
              if (!results[envName]) results[envName] = {};

              if (Array.isArray(env.secrets)) {
                for (const s of env.secrets) {
                  if (s.name && s.value) {
                    results[envName][s.name] = this.decryptSecret(s.value);
                  }
                }
              }
            }
          }
        }
      }
    } catch {
      // ignore read/parse errors
    }

    return results;
  }
}
