import type { ReasoningLevel, ReasoningOption } from "../types";

export const defaultReasoningLevel: ReasoningLevel = "max";

export const reasoningLabels: Record<ReasoningLevel, string> = {
  none: "关闭",
  low: "轻度",
  medium: "中",
  high: "高",
  xhigh: "很高",
  max: "极高",
};

export const reasoningOptions: ReasoningOption[] = [
  {
    id: "none",
    label: reasoningLabels.none,
    description: "不请求模型进行额外推理",
    budget: {
      contextPolicy: "minimal",
      cachePolicy: "reuse-prefix",
      planner: false,
    },
  },
  {
    id: "low",
    label: reasoningLabels.low,
    description: "简单问答、轻量编辑、低成本优先",
    budget: {
      contextPolicy: "minimal",
      cachePolicy: "reuse-prefix",
      planner: false,
    },
  },
  {
    id: "medium",
    label: reasoningLabels.medium,
    description: "普通代码修改、单文件任务",
    budget: {
      contextPolicy: "task-summary",
      cachePolicy: "reuse-prefix",
      planner: false,
    },
  },
  {
    id: "high",
    label: reasoningLabels.high,
    description: "跨文件修改、复杂 bug、测试修复",
    budget: {
      contextPolicy: "expanded",
      cachePolicy: "stable-prefix",
      planner: true,
    },
  },
  {
    id: "xhigh",
    label: reasoningLabels.xhigh,
    description: "大型实现、深入排查、多阶段验证",
    budget: {
      contextPolicy: "full-relevant",
      cachePolicy: "strict-stable-prefix",
      planner: true,
    },
  },
  {
    id: "max",
    label: reasoningLabels.max,
    description: "协议设计、Agent 架构、发布级检查",
    budget: {
      contextPolicy: "full-relevant",
      cachePolicy: "strict-stable-prefix",
      planner: true,
    },
  },
];
