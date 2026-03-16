export function formatTime(t) {
  try {
    return new Date(t).toLocaleTimeString('en-GB', { hour12: false });
  } catch {
    return '';
  }
}

export function formatElapsed(start, end) {
  try {
    const sec = Math.floor(((end ? new Date(end) : new Date()) - new Date(start)) / 1000);
    if (sec < 0) return '0s';
    if (sec < 60) return `${sec}s`;
    return `${Math.floor(sec / 60)}m${sec % 60}s`;
  } catch {
    return '';
  }
}

export function shortenPath(cwd) {
  if (!cwd) return '(unknown)';
  const parts = cwd.split('/').filter(Boolean);
  if (parts.length <= 3) return cwd;
  return '\u2026/' + parts.slice(-3).join('/');
}
