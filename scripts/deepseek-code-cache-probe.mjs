import { appendFileSync, mkdirSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

const apiKey = process.env.DEEPSEEK_API_KEY;
if (!apiKey) {
  throw new Error("DEEPSEEK_API_KEY is required");
}

const args = new Map();
for (let i = 2; i < process.argv.length; i += 2) {
  args.set(process.argv[i], process.argv[i + 1]);
}

const baseUrl = args.get("--base-url") ?? "https://api.deepseek.com";
const requestedModel = args.get("--model") ?? "deepseek-v4-flash";
const rounds = Number(args.get("--rounds") ?? "8");
const intervalMs = Number(args.get("--interval-ms") ?? "4500");
const thinkingMode = args.get("--thinking") ?? "disabled";
const reasoningEffort = args.get("--reasoning-effort") ?? "";
const maxTokens = Number(args.get("--max-tokens") ?? "256");
const outDir = resolve(args.get("--out-dir") ?? ".cache-experiments");
const startedAt = new Date();
const runId = `code-cache-${startedAt.toISOString().replace(/[:.]/g, "-")}`;
const jsonlPath = join(outDir, `${runId}.jsonl`);
const summaryPath = join(outDir, `${runId}.summary.json`);

mkdirSync(outDir, { recursive: true });

const sleep = (ms) => new Promise((resolveSleep) => setTimeout(resolveSleep, ms));

const codeSample = `#include <iostream>
#include <vector>
#include <chrono>

constexpr size_t ARRAY_SIZE = 1024 * 1024 * 256;
constexpr size_t LOOP_ROUND = 10;

int high_cache_hit() {
    std::vector<int> arr(ARRAY_SIZE, 1);
    long long sum = 0;
    for (size_t r = 0; r < LOOP_ROUND; ++r) {
        // 顺序连续访问，CPU预取生效
        for (size_t i = 0; i < ARRAY_SIZE; ++i) {
            sum += arr[i];
        }
    }
    return sum;
}

int main() {
    auto st = std::chrono::high_resolution_clock::now();
    high_cache_hit();
    auto ed = std::chrono::high_resolution_clock::now();
    auto ms = std::chrono::duration_cast<std::chrono::milliseconds>(ed - st).count();
    std::cout << "High cache hit time: " << ms << "ms\\n";
    return 0;
}`;

const shortSystem = "You are a concise C++ review assistant. Answer in Chinese.";

function longSystem(seed = "stable") {
  const lines = [
    "You are EchoCode's DeepSeek cache probe assistant.",
    "Answer in Chinese, concise and practical.",
    "The following standing instructions are intentionally stable across requests.",
  ];
  for (let i = 0; i < 160; i++) {
    lines.push(
      `stable_prefix_${String(i).padStart(3, "0")}_${seed}: keep role, code-review rubric, cache policy, message order, and output boundary unchanged.`
    );
  }
  return lines.join("\n");
}

const firstPrompt = `看看这段 C++ 代码，先说你看到了什么：\n\n${codeSample}`;
const secondPrompt = "你没发现代码有问题吗？";
const directBugPrompt = `只给出这段 C++ 代码中最关键的一个问题，不要展开解释：\n\n${codeSample}`;

async function listModels() {
  const response = await fetch(`${baseUrl}/models`, {
    headers: { Authorization: `Bearer ${apiKey}` },
  });
  if (!response.ok) {
    return [];
  }
  const body = await response.json();
  return Array.isArray(body.data) ? body.data.map((item) => item.id).filter(Boolean) : [];
}

function chooseModel(models) {
  if (models.includes(requestedModel)) {
    return requestedModel;
  }
  for (const candidate of ["deepseek-v4-flash", "deepseek-chat", "deepseek-reasoner"]) {
    if (models.includes(candidate)) {
      return candidate;
    }
  }
  return requestedModel;
}

const rows = [];
let turn = 0;

function cacheRate(hit, miss) {
  const total = hit + miss;
  return total > 0 ? Number((hit / total).toFixed(4)) : null;
}

function commonPrefixMessages(previous, current) {
  let count = 0;
  while (
    count < previous.length &&
    count < current.length &&
    previous[count].role === current[count].role &&
    previous[count].content === current[count].content
  ) {
    count++;
  }
  return count;
}

async function chat({ model, scenario, messages, previousMessages = [] }) {
  const requestStarted = Date.now();
  const commonPrefix = commonPrefixMessages(previousMessages, messages);
  const response = await fetch(`${baseUrl}/chat/completions`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${apiKey}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      model,
      messages,
      thinking: { type: thinkingMode },
      ...(thinkingMode === "enabled" && reasoningEffort ? { reasoning_effort: reasoningEffort } : {}),
      temperature: 0,
      max_tokens: maxTokens,
      stream: false,
    }),
  });
  const text = await response.text();
  let body;
  try {
    body = JSON.parse(text);
  } catch {
    body = { raw: text.slice(0, 240) };
  }
  const usage = body.usage ?? {};
  const hit = usage.prompt_cache_hit_tokens ?? 0;
  const miss = usage.prompt_cache_miss_tokens ?? 0;
  const message = body.choices?.[0]?.message ?? {};
  const content = message.content ?? "";
  const reasoningContent = message.reasoning_content ?? "";
  const answerText = content || reasoningContent;
  const row = {
    at: new Date().toISOString(),
    turn,
    scenario,
    status: response.status,
    model,
    latencyMs: Date.now() - requestStarted,
    messageCount: messages.length,
    previousMessageCount: previousMessages.length,
    commonPrefix,
    appendOnlyStable: commonPrefix === previousMessages.length,
    promptTokens: usage.prompt_tokens ?? null,
    completionTokens: usage.completion_tokens ?? null,
    totalTokens: usage.total_tokens ?? null,
    hit,
    miss,
    hitRate: cacheRate(hit, miss),
    thinkingMode,
    reasoningEffort: thinkingMode === "enabled" ? reasoningEffort : "",
    finishReason: body.choices?.[0]?.finish_reason ?? null,
    answerChars: content.length,
    reasoningChars: reasoningContent.length,
    answerMentionsOverflow: /溢出|overflow|int|return|long long|截断|未定义/i.test(answerText),
    error: response.ok ? null : body.error?.message ?? body.raw ?? text.slice(0, 240),
  };
  rows.push(row);
  appendFileSync(jsonlPath, `${JSON.stringify(row)}\n`, "utf8");
  return { row, content: answerText };
}

function summarize(model, models) {
  const byScenario = {};
  for (const row of rows) {
    const bucket =
      byScenario[row.scenario] ??
      (byScenario[row.scenario] = {
        count: 0,
        ok: 0,
        hit: 0,
        miss: 0,
        minHitRate: null,
        maxHitRate: null,
        lastHitRate: null,
        firstHitTurn: null,
        firstAbove96Turn: null,
        prefixBreaks: 0,
        answersMentioningOverflow: 0,
      });
    bucket.count++;
    if (row.status >= 200 && row.status < 300) {
      bucket.ok++;
    }
    bucket.hit += row.hit;
    bucket.miss += row.miss;
    if (row.hitRate != null) {
      bucket.minHitRate = bucket.minHitRate == null ? row.hitRate : Math.min(bucket.minHitRate, row.hitRate);
      bucket.maxHitRate = bucket.maxHitRate == null ? row.hitRate : Math.max(bucket.maxHitRate, row.hitRate);
      bucket.lastHitRate = row.hitRate;
      if (bucket.firstAbove96Turn == null && row.hitRate >= 0.96) {
        bucket.firstAbove96Turn = row.turn;
      }
    }
    if (bucket.firstHitTurn == null && row.hit > 0) {
      bucket.firstHitTurn = row.turn;
    }
    if (!row.appendOnlyStable) {
      bucket.prefixBreaks++;
    }
    if (row.answerMentionsOverflow) {
      bucket.answersMentioningOverflow++;
    }
  }
  for (const bucket of Object.values(byScenario)) {
    const total = bucket.hit + bucket.miss;
    bucket.overallHitRate = total > 0 ? Number((bucket.hit / total).toFixed(4)) : null;
  }
  const summary = {
    startedAt: startedAt.toISOString(),
    updatedAt: new Date().toISOString(),
    requestedModel,
    model,
    availableModels: models,
    rounds,
    intervalMs,
    thinkingMode,
    reasoningEffort: thinkingMode === "enabled" ? reasoningEffort : "",
    maxTokens,
    jsonlPath,
    summaryPath,
    totalRows: rows.length,
    byScenario,
  };
  writeFileSync(summaryPath, `${JSON.stringify(summary, null, 2)}\n`, "utf8");
  return summary;
}

async function run() {
  const models = await listModels();
  const model = chooseModel(models);
  const sessions = {
    twoStageShort: [{ role: "system", content: shortSystem }],
    twoStageLong: [{ role: "system", content: longSystem("code") }],
  };
  const lastRequests = new Map();

  const scenarioOrder = [
    "same_prefix_independent",
    "changed_front_control",
    "two_stage_short_first",
    "two_stage_short_second",
    "two_stage_long_first",
    "two_stage_long_second",
    "direct_bug_short",
  ];

  for (let round = 0; round < rounds; round++) {
    for (const scenario of scenarioOrder) {
      turn++;
      let messages;
      if (scenario === "same_prefix_independent") {
        messages = [
          { role: "system", content: longSystem("code") },
          { role: "user", content: firstPrompt },
        ];
      } else if (scenario === "changed_front_control") {
        messages = [
          { role: "system", content: `control_turn=${turn}\n${longSystem("code")}` },
          { role: "user", content: firstPrompt },
        ];
      } else if (scenario === "two_stage_short_first") {
        messages = sessions.twoStageShort.concat({ role: "user", content: firstPrompt });
      } else if (scenario === "two_stage_short_second") {
        messages = sessions.twoStageShort.concat({ role: "user", content: secondPrompt });
      } else if (scenario === "two_stage_long_first") {
        messages = sessions.twoStageLong.concat({ role: "user", content: firstPrompt });
      } else if (scenario === "two_stage_long_second") {
        messages = sessions.twoStageLong.concat({ role: "user", content: secondPrompt });
      } else if (scenario === "direct_bug_short") {
        messages = [
          { role: "system", content: shortSystem },
          { role: "user", content: directBugPrompt },
        ];
      }

      const previous = lastRequests.get(scenario) ?? [];
      try {
        const { content } = await chat({ model, scenario, messages, previousMessages: previous });
        if (scenario === "two_stage_short_first" || scenario === "two_stage_short_second") {
          sessions.twoStageShort = messages.concat({ role: "assistant", content });
        } else if (scenario === "two_stage_long_first" || scenario === "two_stage_long_second") {
          sessions.twoStageLong = messages.concat({ role: "assistant", content });
        }
      } catch (error) {
        rows.push({
          at: new Date().toISOString(),
          turn,
          scenario,
          status: 0,
          model,
          error: error instanceof Error ? error.message : String(error),
          hit: 0,
          miss: 0,
          hitRate: null,
        });
      }
      lastRequests.set(scenario, messages);
      summarize(model, models);
      await sleep(intervalMs);
    }
  }
  console.log(JSON.stringify(summarize(model, models), null, 2));
}

run().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
