import { describe, expect, test } from "bun:test";

import { appendLiveAssistantText, displayMessageParts, liveTaskStatus, updateLiveTimelineParts } from "../src/lib/timeline";
import type { ChatTaskEvent, MessagePart } from "../src/types";

describe("live task timeline", () => {
  test("keeps provider heartbeats out of the durable activity feed", () => {
    const parts: MessagePart[] = [{
      kind: "timeline_note",
      message: "正在分析任务",
      status: "running",
      startedAt: "2026-07-28T00:00:00Z",
    }];
    const heartbeat: ChatTaskEvent = {
      taskId: "task-1",
      type: "heartbeat",
      status: "waiting",
      message: "上游模型仍在处理（16s 未收到新数据）",
    };

    expect(updateLiveTimelineParts(parts, heartbeat)).toEqual(parts);
    expect(liveTaskStatus(heartbeat.message)).toBe("正在执行任务");
  });

  test("hides routine setup phases while keeping one compact live status", () => {
    const phases = ["正在准备上下文", "正在连接 Anthropic", "正在分析任务", "正在生成执行计划"];
    for (const message of phases) {
      const event: ChatTaskEvent = { taskId: "task-1", type: "status", status: "running", message };
      expect(updateLiveTimelineParts([], event)).toEqual([]);
      expect(liveTaskStatus(message)).toBe("正在执行任务");
    }
  });

  test("persists meaningful phases once", () => {
    const event: ChatTaskEvent = {
      taskId: "task-1",
      type: "status",
      status: "running",
      message: "连接中断，正在重试",
    };
    const first = updateLiveTimelineParts([], event);
    const duplicate = updateLiveTimelineParts(first, event);

	expect(first).toHaveLength(1);
	expect(duplicate).toEqual(first);
	expect(liveTaskStatus(event.message)).toBe("正在执行任务");
  });

  test("deduplicates a progress milestone by tool call identity", () => {
	const first = updateLiveTimelineParts([], {
	  taskId: "task-1",
	  type: "status",
	  status: "running",
	  toolCallId: "progress-1",
	  message: "SSH 已验证，正在读取部署配置。",
	});
	const duplicate = updateLiveTimelineParts(first, {
	  taskId: "task-1",
	  type: "status",
	  status: "completed",
	  toolCallId: "progress-1",
	  message: "SSH 已验证，正在读取部署配置。",
	});

	expect(duplicate[0]).toMatchObject({ toolCallId: "progress-1", status: "completed" });
  });

  test("settles the previous milestone when a new milestone starts", () => {
	const first = updateLiveTimelineParts([], {
	  taskId: "task-1", type: "status", status: "running", toolCallId: "progress-1", message: "已连接服务器。",
	});
	const second = updateLiveTimelineParts(first, {
	  taskId: "task-1", type: "status", status: "running", toolCallId: "progress-2", message: "正在读取配置。",
	});
	expect(second[0]).toMatchObject({ status: "completed" });
	expect(second[1]).toMatchObject({ status: "running" });
  });

  test("keeps streamed progress text in chronological order before an activity", () => {
    const parts: MessagePart[] = [{
      kind: "timeline_note",
      message: "正在分析任务",
      status: "running",
    }];
    const flushed = appendLiveAssistantText(parts, "我先核对目标机上的运行服务和实际配置。");

    expect(flushed).toEqual([
      parts[0],
      { kind: "text", text: "我先核对目标机上的运行服务和实际配置。" },
    ]);
    expect(appendLiveAssistantText(flushed, "我先核对目标机上的运行服务和实际配置。")).toEqual(flushed);
  });

  test("previews the current streamed text after existing activity parts", () => {
    const parts: MessagePart[] = [{ kind: "tool_call", name: "ssh", status: "ok" }];
    expect(displayMessageParts(parts, "已经找到实际配置文件。", true)).toEqual([
      parts[0],
      { kind: "text", text: "已经找到实际配置文件。" },
    ]);
    expect(displayMessageParts(parts, "最终答复", false)).toEqual(parts);
  });

  test("never renders tagged private reasoning or duplicated progress arguments", () => {
    const parts: MessagePart[] = [
      { kind: "text", text: "<thinking>private English reasoning</thinking>\n用户可见进展。" },
      { kind: "timeline_note", message: "已定位配置，正在读取字段。", status: "running" },
      { kind: "text", text: JSON.stringify({ message: "已定位配置，正在读取字段。", status: "running" }) },
    ];
    expect(displayMessageParts(parts, "", false)).toEqual([
      { kind: "text", text: "用户可见进展。" },
      parts[1],
    ]);
    expect(displayMessageParts(undefined, "<analysis>unfinished private reasoning", true)).toEqual([]);
	expect(displayMessageParts([
	  { kind: "text", text: JSON.stringify({ message: "内部进展参数", status: "running" }) },
	], "", false)).toEqual([]);
	expect(displayMessageParts(undefined, JSON.stringify({ message: "流式进展参数", status: "waiting" }), true)).toEqual([]);
	expect(displayMessageParts([
	  { kind: "timeline_note", message: "正在验证配置。", status: "running" },
	], JSON.stringify({ message: "正在验证配置。", status: "running" }), true)).toEqual([
	  { kind: "timeline_note", message: "正在验证配置。", status: "running" },
	]);
  });
});
