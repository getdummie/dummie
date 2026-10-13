import { randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { streamSimple as streamAnthropic } from "@earendil-works/pi-ai/api/anthropic-messages";
import { streamSimple as streamCompletions } from "@earendil-works/pi-ai/api/openai-completions";
import { streamSimple as streamResponses } from "@earendil-works/pi-ai/api/openai-responses";
import { opencodeGoProvider } from "@earendil-works/pi-ai/providers/opencode-go";

type ProxyConfig = { baseUrl: string; host: string };

const config = JSON.parse(readFileSync(new URL("./config.json", import.meta.url), "utf8")) as ProxyConfig;
const baseUrl = config.baseUrl.replace(/\/+$/, "");
const anthropicBaseUrl = baseUrl.replace(/\/v1$/, "");
const proxyOrigin = new URL(baseUrl).origin;
const fallbackIDs = ["chatgpt/gpt-6-sol", "chatgpt/gpt-6-astra", "chatgpt/gpt-6-luna"];
const opencodePrefix = "opencode-go/";
const opencodeProvider = opencodeGoProvider();
const opencodeModels = new Map(opencodeProvider.getModels().map((entry) => [entry.id, entry]));
const opencodeSession = randomUUID();

function responseModel(id: string) {
  return {
    id,
    name: id,
    api: "dummie" as const,
    headers: { Host: config.host },
    reasoning: false,
    input: ["text"] as ("text" | "image")[],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 128000,
    maxTokens: 16384,
  };
}

function model(id: string) {
  if (!id.startsWith(opencodePrefix)) return responseModel(id);

  const source = opencodeModels.get(id.slice(opencodePrefix.length));
  if (!source) return;
  return {
    id,
    name: source.name,
    api: "dummie" as const,
    baseUrl: source.api === "anthropic-messages" ? anthropicBaseUrl : baseUrl,
    reasoning: source.reasoning,
    thinkingLevelMap: source.thinkingLevelMap,
    input: source.input,
    inputLimits: source.inputLimits,
    cost: source.cost,
    promptCache: source.promptCache,
    contextWindow: source.contextWindow,
    maxTokens: source.maxTokens,
    headers: { ...source.headers, Host: config.host, "x-opencode-session": opencodeSession },
    compat: source.compat,
  };
}

async function discoverModels(signal?: AbortSignal) {
  const timeout = AbortSignal.timeout(3000);
  const response = await Bun.fetch(`${baseUrl}/models`, {
    headers: { Host: config.host },
    signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
  });
  if (!response.ok) throw new Error(`model discovery returned ${response.status}`);
  const body = await response.json() as { data?: { id?: unknown }[] };
  if (!Array.isArray(body.data)) throw new Error("model discovery returned no model list");
  const ids = [...new Set(body.data.map((entry) => entry.id).filter(
    (id): id is string => typeof id === "string" && id.length > 0,
  ))];
  if (ids.length === 0) throw new Error("model discovery returned an empty model list");
  return ids.map(model).filter((entry) => entry !== undefined);
}

export default async function (pi: ExtensionAPI) {
  let models;
  try {
    models = await discoverModels();
  } catch (error) {
    console.warn(`[pi-dummie] model discovery failed: ${(error as Error).message}`);
    models = fallbackIDs.map(responseModel);
  }

  pi.registerProvider("dummie", {
    name: "Dummie LLM proxy",
    baseUrl,
    api: "dummie",
    apiKey: "local-proxy",
    headers: { Host: config.host },
    models,
    refreshModels: async (context) => {
      try {
        models = await discoverModels(context.signal);
      } catch (error) {
        if (!context.signal.aborted) {
          console.warn(`[pi-dummie] model refresh failed: ${(error as Error).message}`);
        }
      }
      return models;
    },
    streamSimple: (selectedModel, transcript, options) => {
      const isOpenCode = selectedModel.id.startsWith(opencodePrefix);
      const api = isOpenCode
        ? opencodeModels.get(selectedModel.id.slice(opencodePrefix.length))?.api
        : "openai-responses";
      const proxyOptions = {
        ...options,
        onPayload: async (payload, requestModel) => {
          const transformed = await options?.onPayload?.(payload, requestModel);
          const body = transformed ?? payload;
          return typeof body === "object" && body !== null
            ? { ...body, model: selectedModel.id }
            : body;
        },
        fetch: async (input, init) => {
          const request = new Request(input, init);
          const url = new URL(request.url);
          if (url.origin !== proxyOrigin) throw new Error("Dummie model request left the proxy origin");
          const headers = new Headers(request.headers);
          headers.set("Host", config.host);
          if (isOpenCode && !headers.has("x-opencode-session")) {
            headers.set("x-opencode-session", options?.sessionId ?? opencodeSession);
          }
          if (isOpenCode && request.method === "POST") {
            const body = await request.clone().json() as Record<string, unknown>;
            body.model = selectedModel.id;
            headers.delete("content-length");
            return Bun.fetch(request.url, {
              method: request.method,
              headers,
              body: JSON.stringify(body),
              signal: request.signal,
            });
          }
          return Bun.fetch(request, { headers });
        },
      };
      switch (api) {
        case "anthropic-messages":
          return streamAnthropic({ ...selectedModel, api }, transcript, proxyOptions);
        case "openai-completions":
          return streamCompletions({ ...selectedModel, api }, transcript, proxyOptions);
        case "openai-responses":
          return streamResponses({ ...selectedModel, api: "openai-responses" }, transcript, proxyOptions);
        default:
          throw new Error(`Dummie model ${selectedModel.id} uses unsupported protocol ${api ?? "unknown"}`);
      }
    },
  });
}
