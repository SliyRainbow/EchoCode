export function errorMessage(err: unknown): string {
  const message = err instanceof Error ? err.message : String(err);
  if (
    message.toLowerCase().includes("chat task is running; stop it before") ||
    message.includes("对话任务正在运行") ||
    message.includes("已有对话任务正在运行")
  ) {
    return "";
  }
  return message;
}
