import { render } from "solid-js/web";
import App from "./app";
import "./styles.css";
import "highlight.js/styles/github-dark.css";
import "./styles/chat.css";
import "./styles/polish.css";

render(() => <App />, document.getElementById("root") as HTMLElement);
