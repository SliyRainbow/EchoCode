import { Button } from "@kobalte/core/button";
import { Eye, EyeOff } from "lucide-solid";
import { Show, createEffect, createSignal } from "solid-js";

export function SecretInput(props: { label: string; scope: string; value: string; placeholder?: string; disabled?: boolean; onChange: (value: string) => void }) {
  const [visible, setVisible] = createSignal(false);
  createEffect(() => { props.scope; setVisible(false); });
  return (
    <div class="settings-secret-input">
      <input aria-label={props.label} type={visible() ? "text" : "password"} autocomplete="off" spellcheck={false} value={props.value} placeholder={props.placeholder} disabled={props.disabled} onInput={event => props.onChange(event.currentTarget.value)} />
      <Button aria-label={visible() ? `隐藏${props.label}` : `显示${props.label}`} aria-pressed={visible()} disabled={props.disabled} onClick={() => setVisible(value => !value)}><Show when={visible()} fallback={<Eye size={15} />}><EyeOff size={15} /></Show></Button>
    </div>
  );
}
