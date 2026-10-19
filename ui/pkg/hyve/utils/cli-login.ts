// The hyve CLI's browser sign-in (`hyve login --browser`, and the default
// for Rancher auth providers that Rancher's own CLI can't sign in: GitHub,
// Google, OIDC). The CLI listens on a localhost port and opens this page
// with the port, a state to echo back and an RSA public key. After the user
// confirms, the page creates a Rancher API key for them, encrypts it with
// the public key and sends the browser to the CLI's callback with it.
//
// The contract is the CLI's (hyve internal/login/browser.go, CLILoginPath):
//   query      port, state, publicKey (base64url RSA SPKI DER)
//   callback   http://127.0.0.1:<port>/callback?state=…&token=<base64url
//              RSA-OAEP/SHA-256 ciphertext>&expiresAt=<RFC 3339, or empty>
//              or ?state=…&error=<reason>
//
// Nothing here touches the store, so it can be tested on its own.

export const CLI_LOGIN_ROUTE = 'hyve-cli-login';

// Under Rancher's /dashboard/ base. It has no /c/<cluster>/ part on purpose:
// a cluster in the path makes the shell load that cluster, and users without
// access to it would be sent home instead of reaching the page.
export const CLI_LOGIN_PATH = '/hyve/cli-login';

// The request is kept here across Rancher's own sign-in, which for GitHub,
// Google and OIDC leaves the dashboard and comes back to its home page.
export const STASH_KEY = 'hyve-cli-login';
const STASH_MAX_AGE_MS = 10 * 60 * 1000;

const MIN_MODULUS_BITS = 2048;
const MAX_STATE_LENGTH = 256;

export interface CliLoginRequest {
  port: number;
  state: string;
  publicKey: string;
}

export class CliLoginError extends Error {}

type Query = Record<string, unknown>;

function one(q: Query, name: string): string {
  const v = q[name];

  return typeof v === 'string' ? v : (Array.isArray(v) && typeof v[0] === 'string' ? v[0] : '');
}

// parseRequest checks the query the CLI opened the page with. It doesn't
// check the key itself; importPublicKey does.
export function parseRequest(q: Query): CliLoginRequest {
  const portText = one(q, 'port');
  const state = one(q, 'state');
  const publicKey = one(q, 'publicKey');

  if (!portText || !state || !publicKey) {
    throw new CliLoginError('This page is opened by `hyve login`; the link is missing its port, state or key.');
  }
  if (!/^[0-9]{1,5}$/.test(portText) || +portText < 1 || +portText > 65535) {
    throw new CliLoginError(`"${ portText }" isn't a port.`);
  }
  if (state.length > MAX_STATE_LENGTH || !/^[A-Za-z0-9_-]+$/.test(state)) {
    throw new CliLoginError('The sign-in link has a malformed state.');
  }
  if (!/^[A-Za-z0-9_-]+$/.test(publicKey)) {
    throw new CliLoginError('The sign-in link has a malformed key.');
  }

  return { port: +portText, state, publicKey };
}

export function base64urlDecode(s: string): Uint8Array {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - s.length % 4) % 4);
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);

  for (let i = 0; i < bin.length; i++) {
    out[i] = bin.charCodeAt(i);
  }

  return out;
}

export function base64urlEncode(bytes: ArrayBuffer | Uint8Array): string {
  const u8 = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  let bin = '';

  for (let i = 0; i < u8.length; i++) {
    bin += String.fromCharCode(u8[i]);
  }

  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export async function importPublicKey(publicKey: string, subtle: SubtleCrypto = globalThis.crypto.subtle): Promise<CryptoKey> {
  let key: CryptoKey;

  try {
    key = await subtle.importKey('spki', base64urlDecode(publicKey), { name: 'RSA-OAEP', hash: 'SHA-256' }, false, ['encrypt']);
  } catch (e) {
    throw new CliLoginError('The key in the sign-in link isn\'t an RSA public key.');
  }
  if ((key.algorithm as RsaHashedKeyAlgorithm).modulusLength < MIN_MODULUS_BITS) {
    throw new CliLoginError(`The key in the sign-in link is shorter than ${ MIN_MODULUS_BITS } bits.`);
  }

  return key;
}

export async function encryptToken(key: CryptoKey, token: string, subtle: SubtleCrypto = globalThis.crypto.subtle): Promise<string> {
  const ct = await subtle.encrypt({ name: 'RSA-OAEP' }, key, new TextEncoder().encode(token));

  return base64urlEncode(ct);
}

// callbackURL is always 127.0.0.1, whatever the link says, so the token can
// only go to this machine.
export function callbackURL(req: CliLoginRequest, params: Record<string, string>): string {
  const q = new URLSearchParams({ state: req.state, ...params });

  return `http://127.0.0.1:${ req.port }/callback?${ q.toString() }`;
}

// sessionTTLMillis is how long the CLI's API key lasts: Rancher's session
// TTL (auth-user-session-ttl-minutes), so a CLI sign-in lasts as long as a
// browser one. Rancher caps it at auth-token-max-ttl-minutes itself.
export function sessionTTLMillis(settingValue: unknown, fallbackMinutes = 960): number {
  const minutes = parseInt(`${ settingValue ?? '' }`, 10);

  return (Number.isFinite(minutes) && minutes > 0 ? minutes : fallbackMinutes) * 60 * 1000;
}

interface Stash {
  search: string;
  at: number;
}

type Storage = Pick<globalThis.Storage, 'getItem' | 'setItem' | 'removeItem'>;

function storage(): Storage | null {
  try {
    return globalThis.sessionStorage ?? null;
  } catch (e) {
    return null;
  }
}

// stashRequest keeps the page's query when the dashboard loads on the page,
// before Rancher's sign-in can send the browser away.
export function stashRequest(pathname: string, search: string, now = Date.now(), store: Storage | null = storage()): void {
  if (!store || !search || !pathname.replace(/\/+$/, '').endsWith(CLI_LOGIN_PATH)) {
    return;
  }
  try {
    store.setItem(STASH_KEY, JSON.stringify({ search, at: now } as Stash));
  } catch (e) {}
}

// stashedQuery returns a recent stashed query, or null.
export function stashedQuery(now = Date.now(), store: Storage | null = storage()): Record<string, string> | null {
  try {
    const s = JSON.parse(store?.getItem(STASH_KEY) || 'null') as Stash | null;

    if (!s || typeof s.search !== 'string' || !(now - s.at < STASH_MAX_AGE_MS)) {
      return null;
    }

    return Object.fromEntries(new URLSearchParams(s.search));
  } catch (e) {
    return null;
  }
}

export function clearStash(store: Storage | null = storage()): void {
  try {
    store?.removeItem(STASH_KEY);
  } catch (e) {}
}
