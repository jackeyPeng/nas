// Z1 NAS Service Worker — PWA offline shell
const CACHE = 'z1-nas-v1';
const SHELL = [
  '/',
  '/style.css',
  '/alpinejs.min.js',
  '/i18n.js',
  '/i18n/zh-CN.json',
  '/i18n/en-US.json',
  '/manifest.json'
];

self.addEventListener('install', e => {
  e.waitUntil(caches.open(CACHE).then(c => c.addAll(SHELL)));
});

self.addEventListener('fetch', e => {
  // API calls bypass cache
  if (e.request.url.includes('/api/')) return;
  e.respondWith(
    caches.match(e.request).then(r => r || fetch(e.request))
  );
});