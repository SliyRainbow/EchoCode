export function formatElapsedDuration(durationMs?: number) {
  if (!Number.isFinite(durationMs) || (durationMs ?? 0) < 0) {
    return "";
  }
  const totalSeconds = Math.max(0, Math.round((durationMs ?? 0) / 1000));
  if (totalSeconds < 60) {
    return `${totalSeconds}s`;
  }
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) {
    return `${hours}h ${minutes}m ${seconds}s`;
  }
  return `${minutes}m ${seconds}s`;
}
