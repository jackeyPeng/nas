// Z1 NAS Service Worker — PWA offline shell
// 策略：网络优先 + 写穿缓存（在线永远拿新版，杜绝升级后 PWA 陈旧），
// 断网时回退缓存；activate 时清理旧版本缓存，避免 caches.match 命中过期副本。
const CACHE = 'z1-nas-v4';
const SHELL = [
  '/',
  '/style.css',
  '/app.js',
  '/alpinejs.min.js',
  '/zkvault.min.js',
  '/i18n.js',
  '/i18n/zh-CN.json',
  '/i18n/en-US.json',
  '/manifest.json',
  '/favicon.ico',
  '/logo.svg'
];

self.addEventListener('install', e => {
  e.waitUntil(caches.open(CACHE).then(c => c.addAll(SHELL)));
  self.skipWaiting();
});

self.addEventListener('activate', e => {
  e.waitUntil(
    caches.keys()
      .then(keys => Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', e => {
  if (e.request.method !== 'GET') return;
  const url = new URL(e.request.url);
  // API 与跨域请求不拦截
  if (url.origin !== location.origin || url.pathname.startsWith('/api/')) return;
  e.respondWith(
    fetch(e.request)
      .then(resp => {
        const copy = resp.clone();
        caches.open(CACHE).then(c => c.put(e.request, copy));
        return resp;
      })
      .catch(() => caches.match(e.request).then(r => r || caches.match('/')))
  );
});
