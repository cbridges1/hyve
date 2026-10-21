import { reactive } from 'vue';

// Who's signed in, and the last error a page's fetch hit.
export const session = reactive({ user: null, version: '', error: '' });

// The server the CLI saved for this host, when 'hyve ui' opened the page:
// kubeconfigs from the UI run 'hyve token --server' for it. Without one,
// the page's own address is the host's.
const CLI_SERVER = 'hyve.cliServer';

export function rememberCLIServer(name, url) {
  try {
    window.sessionStorage.setItem(CLI_SERVER, JSON.stringify({ name, url }));
  } catch (e) {}
}

export function cliServer() {
  try {
    const s = JSON.parse(window.sessionStorage.getItem(CLI_SERVER) || 'null');

    if (s?.name && s?.url) {
      return s;
    }
  } catch (e) {}

  return { name: window.location.origin, url: window.location.origin };
}
