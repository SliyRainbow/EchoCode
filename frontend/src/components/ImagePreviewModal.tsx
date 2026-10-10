import { Download, Maximize2, Minimize2, X } from "lucide-solid";
import { Dialog } from "@kobalte/core/dialog";
import { createSignal, onCleanup, onMount } from "solid-js";
import type { ChatAttachment } from "../types";

export function ImagePreviewModal(props: {
  attachment: ChatAttachment;
  onClose: () => void;
}) {
  const [actualSize, setActualSize] = createSignal(false);
  const [dimensions, setDimensions] = createSignal("");
  const [fitSize, setFitSize] = createSignal({ width: 0, height: 0 });
  let stageRef: HTMLDivElement | undefined;
  let naturalWidth = 0;
  let naturalHeight = 0;

  const imageURL = () => `data:${props.attachment.mimeType};base64,${props.attachment.data}`;
  const updateFitSize = () => {
    if (!stageRef || naturalWidth <= 0 || naturalHeight <= 0) return;
    const availableWidth = Math.max(1, stageRef.clientWidth - 48);
    const availableHeight = Math.max(1, stageRef.clientHeight - 48);
    const scale = Math.min(4, availableWidth / naturalWidth, availableHeight / naturalHeight);
    setFitSize({
      width: Math.max(1, Math.round(naturalWidth * scale)),
      height: Math.max(1, Math.round(naturalHeight * scale)),
    });
  };

  onMount(() => {
    const resizeObserver = new ResizeObserver(updateFitSize);
    if (stageRef) resizeObserver.observe(stageRef);
    onCleanup(() => resizeObserver.disconnect());
  });

  return (
    <Dialog open onOpenChange={(open) => { if (!open) props.onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay class="ui-modal-backdrop" />
        <div class="ui-modal-positioner image-preview-positioner">
      <Dialog.Content
        class="image-viewer-dialog"
        aria-label={`查看图片 ${props.attachment.name}`}
      >
        <Dialog.Title class="sr-only">查看图片 {props.attachment.name}</Dialog.Title>
        <Dialog.Description class="sr-only">可以切换原始尺寸、下载图片，或按 Escape 关闭。</Dialog.Description>
        <header class="image-viewer-head">
          <div class="image-viewer-meta">
            <strong title={props.attachment.name}>{props.attachment.name}</strong>
            <span>{dimensions() || props.attachment.mimeType}</span>
          </div>
          <div class="image-viewer-actions">
            <button
              type="button"
              title={actualSize() ? "适应窗口" : "按原始尺寸查看"}
              aria-label={actualSize() ? "适应窗口" : "按原始尺寸查看"}
              onClick={() => setActualSize((current) => !current)}
            >
              {actualSize() ? <Minimize2 size={16} /> : <Maximize2 size={16} />}
            </button>
            <a href={imageURL()} download={props.attachment.name} title="下载图片" aria-label="下载图片">
              <Download size={16} />
            </a>
            <button type="button" title="关闭图片" aria-label="关闭图片" onClick={props.onClose}>
              <X size={17} />
            </button>
          </div>
        </header>
        <div ref={stageRef} class="image-viewer-stage" classList={{ actual: actualSize() }}>
          <div class="image-viewer-canvas">
            <img
              src={imageURL()}
              alt={props.attachment.name}
              draggable={false}
              title={actualSize() ? "双击适应窗口" : "双击按原始尺寸查看"}
              style={{
                width: actualSize() || fitSize().width === 0 ? "auto" : `${fitSize().width}px`,
                height: actualSize() || fitSize().height === 0 ? "auto" : `${fitSize().height}px`,
              }}
              onDblClick={() => setActualSize((current) => !current)}
              onLoad={(event) => {
                const image = event.currentTarget;
                naturalWidth = image.naturalWidth;
                naturalHeight = image.naturalHeight;
                setDimensions(`${image.naturalWidth} × ${image.naturalHeight}`);
                updateFitSize();
              }}
            />
          </div>
        </div>
      </Dialog.Content>
        </div>
      </Dialog.Portal>
    </Dialog>
  );
}
