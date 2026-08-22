// TIL 서비스 워커.
// 정적 자원은 stale-while-revalidate, 페이지는 network-first + 캐시 폴백.
// 자원 파일명에 해시를 붙이는 빌드 단계가 없으므로, 캐시 이름은 활성화 시
// 옛 캐시를 지우는 용도로만 쓰고 신선도는 SWR 이 매 요청마다 챙긴다.
const CACHE = 'til-v1';
const OFFLINE_URL = '/static/offline.html';

const PRECACHE = [
  OFFLINE_URL,
  '/static/style.css',
  '/static/htmx.min.js',
  '/static/manifest.webmanifest',
  '/static/icon-192.png',
  '/static/icon-512.png',
  '/static/icon-maskable-512.png',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE)
      // 한 파일이 실패해도 설치 전체가 무산되지 않도록 개별로 담는다.
      .then((cache) => Promise.all(PRECACHE.map((url) => cache.add(url).catch(() => {}))))
      .then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;

  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  if (req.mode === 'navigate') {
    event.respondWith(networkFirst(req));
    return;
  }
  if (url.pathname.startsWith('/static/')) {
    event.respondWith(staleWhileRevalidate(req));
  }
  // 그 밖의 GET(htmx 조각, /healthz)은 캐시하지 않고 그대로 네트워크로 보낸다.
});

// 페이지: 네트워크를 먼저 시도하고 성공하면 사본을 남긴다.
// 실패하면 마지막으로 받아둔 같은 페이지를, 그것도 없으면 오프라인 안내를 보여준다.
async function networkFirst(req) {
  const cache = await caches.open(CACHE);
  try {
    const res = await fetch(req);
    if (res.ok && res.type === 'basic') cache.put(req, res.clone());
    return res;
  } catch (err) {
    return (await cache.match(req)) || (await cache.match(OFFLINE_URL)) || Response.error();
  }
}

// 정적 자원: 캐시본을 바로 내주고 뒤에서 조용히 갱신한다.
async function staleWhileRevalidate(req) {
  const cache = await caches.open(CACHE);
  const cached = await cache.match(req);
  const fresh = fetch(req)
    .then((res) => {
      if (res.ok && res.type === 'basic') cache.put(req, res.clone());
      return res;
    })
    .catch(() => cached);
  return cached || fresh;
}
