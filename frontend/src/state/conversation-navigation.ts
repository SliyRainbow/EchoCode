export type ConversationView = "projects" | "recent";
const key = "echocode:conversation-view";
export function readConversationView(): ConversationView {
  try { return localStorage.getItem(key) === "recent" ? "recent" : "projects"; } catch { return "projects"; }
}
export function persistConversationView(view: ConversationView): void {
  try { localStorage.setItem(key, view); } catch { /* In-memory navigation remains available. */ }
}
