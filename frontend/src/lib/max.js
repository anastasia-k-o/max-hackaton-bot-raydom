// MAX Bridge (window.WebApp): https://dev.max.ru/docs/webapps/bridge

const wa = () => (typeof window !== 'undefined' ? window.WebApp : undefined);

export const isInsideMax = () => Boolean(wa()?.initData);

/** Без проверки подписи — доверять можно только ответу сервера. */
export function getMaxUser() {
  const u = wa()?.initDataUnsafe?.user;
  if (!u) return null;
  return { maxUserId: u.id, firstName: u.first_name, lastName: u.last_name, photoUrl: u.photo_url };
}

export const getInitData = () => wa()?.initData || '';

export const getStartParam = () => wa()?.initDataUnsafe?.start_param || null;

export async function share(text, link) {
  const w = wa();
  if (w?.shareMaxContent) return w.shareMaxContent({ text, link });
  if (navigator.share) return navigator.share({ title: text, url: link }).catch(() => {});
  await navigator.clipboard?.writeText(link).catch(() => {});
  return 'copied';
}

export function openLink(url) {
  const w = wa();
  if (w?.openLink) w.openLink(url);
  else window.open(url, '_blank', 'noopener');
}

// Упрощённая верификация подтверждённым номером; Цифровой ID — при внедрении.
export async function requestContact() {
  const w = wa();
  if (w?.requestContact) {
    try {
      const contact = await w.requestContact();
      return contact?.phone ? contact : null;
    } catch {
      return null;
    }
  }
  await new Promise((r) => setTimeout(r, 400));
  return { phone: 'demo', authDate: Math.floor(Date.now() / 1000), hash: 'demo' };
}

export const botUrl = () => import.meta.env.VITE_MAX_BOT_URL || '';

export function haptic(type = 'light') {
  try { wa()?.HapticFeedback?.impactOccurred?.(type); } catch { /* noop */ }
}
