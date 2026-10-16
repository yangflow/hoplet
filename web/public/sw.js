// The Hoplet service worker (ADR 0021): it only shows Web Push notifications and opens Hoplet when one is
// clicked. It caches nothing and intercepts no request. A notification has no action buttons: a click opens
// the action page or the session, where the answer is given.

const TEXT = {
  en: {
    permission: 'Permission needed',
    plan: 'Plan ready',
    done: 'Done',
    failure: 'Failed',
    test: 'Test notification',
  },
  'zh-Hans': {
    permission: '需要你的许可',
    plan: '计划待审阅',
    done: '已完成',
    failure: '失败',
    test: '测试通知',
  },
};

function words(language) {
  return String(language || '')
    .toLowerCase()
    .startsWith('zh')
    ? TEXT['zh-Hans']
    : TEXT.en;
}

/** The notification a push payload (NotifyPayload) becomes: title, body and the tag that replaces an earlier one. */
function describe(payload, language) {
  const w = words(language);
  const project = typeof payload.project === 'string' ? payload.project : '';
  const title = typeof payload.title === 'string' ? payload.title : '';
  const kind = payload.kind;
  const tag = payload.requestId
    ? `request-${payload.requestId}`
    : `${kind}-${payload.sessionId || project}`;
  let head;
  let body;
  if (kind === 'permission') {
    head = w.permission;
    body = [payload.tool, payload.summary].filter(Boolean).join(' · ') || title;
  } else if (kind === 'plan') {
    head = w.plan;
    body = title;
  } else if (kind === 'turn-done') {
    head = w.done;
    body = project;
  } else {
    head = w.failure;
    body = payload.reason || project;
  }
  const subject = kind === 'permission' || kind === 'plan' ? project : title || project;
  return {
    title: subject ? `${head} · ${subject}` : head,
    options: {
      body: body || '',
      tag,
      data: { link: payload.link || null, sessionId: payload.sessionId || null },
    },
  };
}

/** Where a click goes on this origin: the action page of the link, else the session, else the app. */
function target(data, origin) {
  if (data && typeof data.link === 'string') {
    try {
      const url = new URL(data.link);
      if (url.pathname === '/a') return `${origin}/a${url.hash}`;
    } catch {
      // Fall through to the session.
    }
  }
  if (data && typeof data.sessionId === 'string' && data.sessionId)
    return `${origin}/?session=${encodeURIComponent(data.sessionId)}`;
  return `${origin}/`;
}

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));

self.addEventListener('push', (event) => {
  let payload = null;
  try {
    payload = event.data ? event.data.json() : null;
  } catch {
    payload = null;
  }
  if (!payload || typeof payload.kind !== 'string') return;
  const { title, options } = describe(payload, self.navigator?.language);
  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = target(event.notification.data, self.location.origin);
  event.waitUntil(
    (async () => {
      const isAction = url.startsWith(`${self.location.origin}/a`);
      if (!isAction) {
        const open = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
        const mine = open.find((c) => c.url.startsWith(self.location.origin));
        if (mine && 'navigate' in mine) {
          const focused = await mine.focus();
          await (focused || mine).navigate(url);
          return;
        }
      }
      await self.clients.openWindow(url);
    })(),
  );
});

if (typeof globalThis.__hopletSwTest === 'object')
  globalThis.__hopletSwTest.exports = { describe, target };
