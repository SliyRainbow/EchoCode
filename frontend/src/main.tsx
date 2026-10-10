import { render } from "solid-js/web";
import { AppRoot } from "./AppRoot";
import "./styles/index.css";

render(() => <AppRoot />, document.getElementById("root") as HTMLElement);
