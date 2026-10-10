import { SquareTerminal } from "lucide-solid";

export function BrandMark(props: { size?: number; class?: string; strokeWidth?: number }) {
  return <SquareTerminal class={props.class} size={props.size ?? 23} strokeWidth={props.strokeWidth ?? 1.6} aria-hidden="true" />;
}
