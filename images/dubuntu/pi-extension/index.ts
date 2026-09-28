import { readFileSync } from "node:fs";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { streamSimple as streamResponses } from "@earendil-works/pi-ai/api/openai-responses";

type ProxyConfig = { baseUrl: string; host: string };

const config = JSON.parse(readFileSync(new URL("./config.json", import.meta.url), "utf8")) as ProxyConfig;
const baseUrl = config.baseUrl.replace(/\/+$/, "");
const proxyOrigin = new URL(baseUrl).origin;
const fallbackIDs = ["chatgpt/gpt-6-sol", "chatgpt/gpt-6-astra", "chatgpt/gpt-6-luna"];

function model(id: string) {
  return {
    id,
    name: id,
    reasoning: false,
    input: ["text"] as ("text" | "image")[],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 128000,
    maxTokens: 16384,
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
  return ids.map(model);
}

export default async function (pi: ExtensionAPI) {
  let models;
  try {
    models = await discoverModels();
  } catch (error) {
    console.warn(`[pi-dummie] model discovery failed: ${(error as Error).message}`);
    models = fallbackIDs.map(model);
  }

  pi.registerProvider("dummie", {
    name: "Dummie LLM proxy",
    baseUrl,
    api: "openai-responses",
    apiKey: "local-proxy",
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
      return streamResponses(selectedModel, transcript, {
        ...options,
        fetch: (input, init) => {
          const url = new URL(input instanceof Request ? input.url : String(input));
          if (url.origin !== proxyOrigin) throw new Error("Dummie model request left the proxy origin");
          const headers = new Headers(init?.headers);
          headers.set("Host", config.host);
          return Bun.fetch(input, { ...init, headers });
        },
      });
    },
  });
}
