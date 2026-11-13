// hyve's service worker: makes the UI installable and lets the app shell
// open offline. It keeps only what every visitor gets alike: the hashed
// build files under /assets/ and the shell (index.html and the public
// files it loads). It never touches the host's API, sign-in or proxy
// traffic, websockets, or anything but a GET: those go straight to the
// network as if it weren't here, and nothing they return is stored. Signing
// out clears its caches (src/lib/pwa.ts).
//
// The build (vite.config.ts) fills in BUILD, so each build is a new
// worker with its own cache, and PRECACHE, the entry's assets.
const BUILD = '__HYVE_BUILD__'
const PRECACHE = /* __HYVE_PRECACHE__ */ []

const PREFIX = 'hyve-'
const CACHE = PREFIX + BUILD
const SHELL = '/'
const SHELL_FILES = ['/theme.js', '/favicon.svg', '/manifest.json', '/icon.svg', '/icon-192.png', '/icon-512.png', '/apple-touch-icon.png']

// The host's own routes (internal/signin, internal/hostapi and the
// proxies), and OIDC's: never cached, never answered from here, even when
// the browser navigates to one (an identity provider's callback).
const NEVER = ['/v1', '/v1-public', '/v3', '/k8s', '/ping', '/callback', '/token', '/keys', '/jwks', '/.well-known']

function never(path) {
  return NEVER.some((p) => path === p || path.startsWith(p + '/'))
}

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((c) => c.addAll([SHELL, ...SHELL_FILES, ...PRECACHE]))
      .catch(() => {
        // Offline or a file gone: the worker still installs, and caches
        // as pages load.
      }),
  )
  // A new build waits until the page says to take over (an "update
  // available" prompt), so a page never mixes two builds' files.
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k.startsWith(PREFIX) && k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('message', (event) => {
  if (event.data === 'skip-waiting') self.skipWaiting()
})

// Only a 200 for the page's own origin, with nothing marking it private,
// is kept.
function storable(res) {
  if (!res || res.status !== 200 || res.type !== 'basic') return false
  const cc = res.headers.get('Cache-Control') || ''
  return !/no-store|private/i.test(cc) && !res.headers.has('Set-Cookie')
}

self.addEventListener('fetch', (event) => {
  const req = event.request
  if (req.method !== 'GET') return
  const url = new URL(req.url)
  if (url.origin !== self.location.origin || never(url.pathname)) return
  if (req.headers.get('Upgrade')) return

  // Hashed build files never change: the cache first.
  if (url.pathname.startsWith('/assets/')) {
    event.respondWith(
      caches.open(CACHE).then((c) =>
        c.match(req).then(
          (hit) =>
            hit ||
            fetch(req).then((res) => {
              if (storable(res)) c.put(req, res.clone())
              return res
            }),
        ),
      ),
    )
    return
  }

  // A page: the network first (a new build, the host's headers), the
  // stored shell when it can't be reached. Every app route is the same
  // index.html, kept once under '/'.
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req)
        .then((res) => {
          if (storable(res) && (res.headers.get('Content-Type') || '').startsWith('text/html')) {
            const copy = res.clone()
            caches.open(CACHE).then((c) => c.put(SHELL, copy))
          }
          return res
        })
        .catch(() => caches.open(CACHE).then((c) => c.match(SHELL)).then((hit) => hit || Response.error())),
    )
    return
  }

  // The shell's own files: the network first, the cache offline.
  if (SHELL_FILES.includes(url.pathname)) {
    event.respondWith(
      fetch(req)
        .then((res) => {
          if (storable(res)) {
            const copy = res.clone()
            caches.open(CACHE).then((c) => c.put(req, copy))
          }
          return res
        })
        .catch(() => caches.open(CACHE).then((c) => c.match(req)).then((hit) => hit || Response.error())),
    )
  }
})
