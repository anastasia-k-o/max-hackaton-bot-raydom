const icsDate = (d) => d.toISOString().replace(/[-:]/g, '').replace(/\.\d{3}/, '');
const esc = (s = '') => String(s).replace(/([,;\\])/g, '\\$1').replace(/\n/g, '\\n');

/** @param {import('../types/index.js').EventDetails} e */
export function downloadIcs(e) {
  const start = new Date(e.starts_at);
  const end = new Date(start.getTime() + e.duration_min * 60000);
  const body = [
    'BEGIN:VCALENDAR', 'VERSION:2.0', 'PRODID:-//Afisha MAX//RU', 'BEGIN:VEVENT',
    `UID:${e.id}@afisha`, `DTSTAMP:${icsDate(new Date())}`, `DTSTART:${icsDate(start)}`, `DTEND:${icsDate(end)}`,
    `SUMMARY:${esc(e.title)}`, `LOCATION:${esc(e.address)}`, `DESCRIPTION:${esc(e.short_description)}`,
    e.mini_app_url ? `URL:${e.mini_app_url}` : null,
    'END:VEVENT', 'END:VCALENDAR',
  ].filter(Boolean).join('\r\n');
  const url = URL.createObjectURL(new Blob([body], { type: 'text/calendar' }));
  const a = Object.assign(document.createElement('a'), { href: url, download: `${e.title}.ics` });
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
