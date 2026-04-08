const CACHE_VERSION = 'vender-web-v7';
const CORE_FILES = [
  '/__ROOT_PATH__/',
  '/__ROOT_PATH__/index.html',
  '/__ROOT_PATH__/manifest.webmanifest',
  '/__ROOT_PATH__/icon-192.png',
  '/__ROOT_PATH__/icon-512.png',
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

  // API should always be fresh; do not serve from cache.
  if (isAPIRequest) {
    event.respondWith(fetch(event.request));
    return;
  }

  // For HTML shell always try network first to avoid stale UI in installed app.
  if (isNavigation || isIndexLike) {
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

  event.waitUntil(
    Promise.all([
      clickAPI ? fetch(clickAPI, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({
          user_id: data.sender_id || 0,
          user_type: data.sender_type || 0,
          message: data.message || '',
        }),
      }).catch(() => null) : Promise.resolve(),
      clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clientList) => {
        const msg = {
          type: 'notification_click',
          sender_id: data.sender_id || 0,
          sender_type: data.sender_type || 0,
        };
        for (const client of clientList) {
            if ('focus' in client) {
                const url = targetURL + (targetURL.includes('?') ? '&' : '?') +
                    'open_user=' + (data.sender_id || 0) +
                    '&open_user_type=' + (data.sender_type || 0);
                client.navigate(url);
                return client.focus();
            }
        }
        if (clients.openWindow) {
            return clients.openWindow(targetURL + (targetURL.includes('?') ? '&' : '?') + 
                'open_user=' + (data.sender_id || 0) + 
                '&open_user_type=' + (data.sender_type || 0));
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