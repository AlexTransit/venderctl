const CACHE_VERSION = 'vender-web-v6';
const CORE_FILES = [
  '/robot/',
  '/robot/index.html',
  '/robot/manifest.webmanifest',
  '/robot/icon-192.png',
  '/robot/icon-512.png',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_VERSION).then((cache) => cache.addAll(CORE_FILES))
  );
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(
        keys
          .filter((key) => key !== CACHE_VERSION)
          .map((key) => caches.delete(key))
      )
    )
  );
  self.clients.claim();
});

self.addEventListener('fetch', (event) => {
  if (event.request.method !== 'GET') {
    return;
  }

  const reqURL = new URL(event.request.url);
  const isNavigation = event.request.mode === 'navigate';
  const isIndexLike = reqURL.pathname.endsWith('/') || reqURL.pathname.endsWith('/index.html');
  const isSameOrigin = reqURL.origin === self.location.origin;
  const isAPIRequest = isSameOrigin && reqURL.pathname.includes('/api/');
  // app.js и app.css всегда берём свежими — они содержат логику приложения
  const isAppAsset = isSameOrigin && (reqURL.pathname.endsWith('/app.js') || reqURL.pathname.endsWith('/app.css'));

  // API should always be fresh; do not serve from cache.
  if (isAPIRequest) {
    event.respondWith(fetch(event.request));
    return;
  }

  // For HTML shell and app assets always try network first to avoid stale UI in installed app.
  if (isNavigation || isIndexLike || isAppAsset) {
    event.respondWith(
      fetch(event.request)
        .then((response) => {
          if (response && response.status === 200) {
            const copy = response.clone();
            caches.open(CACHE_VERSION).then((cache) => cache.put(event.request, copy));
          }
          return response;
        })
        .catch(() => caches.match(event.request).then((cached) => cached || caches.match('./index.html')))
    );
    return;
  }

  event.respondWith(
    caches.match(event.request).then((cached) => {
      if (cached) {
        return cached;
      }
      return fetch(event.request)
        .then((response) => {
          if (!response || response.status !== 200 || response.type !== 'basic') {
            return response;
          }
          const copy = response.clone();
          caches.open(CACHE_VERSION).then((cache) => cache.put(event.request, copy));
          return response;
        })
        .catch(() => caches.match('./index.html'));
    })
  );
});


self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const data = event.notification.data || {};
  const targetURL = data.url || './';
  const clickAPI = data.click_api;
  const senderId = data.sender_id || 0;
  const senderType = data.sender_type || 0;

  const urlWithParams = senderId > 0
    ? (targetURL + (targetURL.includes('?') ? '&' : '?') +
       'open_user=' + senderId + '&open_user_type=' + senderType)
    : targetURL;

  event.waitUntil(
    Promise.all([
      clickAPI ? fetch(clickAPI, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({
          user_id: senderId,
          user_type: senderType,
          message: data.message || '',
        }),
      }).catch(() => null) : Promise.resolve(),

      clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clientList) => {
        // Если есть открытое окно — посылаем postMessage и фокусируем
        for (const client of clientList) {
          if ('focus' in client) {
            client.postMessage({
              type: 'notification_click',
              sender_id: senderId,
              sender_type: senderType,
            });
            return client.focus();
          }
        }
        // Окна нет — открываем новое с параметрами в URL
        if (clients.openWindow) {
          return clients.openWindow(urlWithParams);
        }
      }),
    ])
  );
});

self.addEventListener('push', (event) => {
  event.waitUntil((async () => {
    let data = {};
    try {
      data = event.data ? event.data.json() : {};
    } catch (_) {
      data = {};
    }

    // If app window is currently visible, skip system notification.
    const windows = await clients.matchAll({ type: 'window', includeUncontrolled: true });
    const hasVisibleClient = windows.some((w) => w.visibilityState === 'visible' || w.focused);
    if (hasVisibleClient) {
      await Promise.all(windows.map((w) => w.postMessage({ type: 'push', payload: data })));
      return;
    }

    const title = data.title || 'Vender Web';
    const body = data.body || 'Новое уведомление';
    const url = data.url || './';
    const icon = './icon-192.png';

    await self.registration.showNotification(title, {
      body,
      icon,
      badge: icon,
      tag: 'vender-push',
      renotify: true,
      data: {
        url,
        click_api: data.click_api,
        sender_id: data.sender_id,
        sender_type: data.sender_type,
        message: data.message,
      },
    });
  })());
});