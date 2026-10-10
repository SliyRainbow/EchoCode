import { ErrorBoundary } from "solid-js";
import App from "./app";

export function AppRoot() {
  return (
    <ErrorBoundary fallback={() => (
      <main class="app-recovery" role="alert">
        <h1>界面暂时无法显示</h1>
        <p>重新加载 EchoCode，恢复已保存的对话。</p>
        <button type="button" onClick={() => window.location.reload()}>重新加载</button>
      </main>
    )}>
      <App />
    </ErrorBoundary>
  );
}
