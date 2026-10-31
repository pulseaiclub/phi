# Supported models

phi talks to LLMs through an **explicit** `api` field on each config entry
(`OpenAI` | `OpenAIResponses` | `Anthropic` | `Gemini`). Empty `api` means
OpenAI-compatible `/chat/completions`. There is no name- or URL-based
provider guessing.

Built-in **presets** supply model capabilities and thinking defaults. Native
IDs also supply connection defaults; gateway aliases need `base_url` and `api`.
Explicit fields override defaults. The configured model ID is sent unchanged.
Source of truth: [`internal/project/model`](../internal/project/model/).

## Gateway aliases and preset bindings

Matching is case-insensitive and accepts routing prefixes, build suffixes and
qualifiers such as `vendor/glm-5.3-flash:free`. A build suffix is a date
(`-20250901`, `-2025-09-01`) or a four-digit tag (`deepseek-v4.1-flash-1017`).
The rightmost recognized model wins; slashes inside `:` or `[...]` qualifiers
are ignored. Specific variants take precedence over their families; arbitrary
variant suffixes do not match.

Also recognized: `group/auto-glm-5-3-flash`, `group/auto-kimi-k3`, and
DeepSeek V4.1 Flash aliases (using the `deepseek-flash` preset).

For an opaque route, `preset` selects capabilities and request hooks:

```yaml
models:
  - name: my-fast-route
    preset: glm-5.3-flash
    api: OpenAI
    base_url: https://gateway.example/v1
    api_key: your-key
```

## Built-in presets

| Name | API | Base URL (default) | Context | Output | Images | Thinking wire |
| ---- | --- | ------------------ | ------: | -----: | :----: | ------------- |
| `gpt-6-astra` | OpenAIResponses | `https://api.openai.com/v1` | 272K | 128K | yes | `reasoning.effort` (max) |
| `gpt-5.6-sol` | OpenAIResponses | `https://api.openai.com/v1` | 272K | 128K | yes | `reasoning.effort` (high) |
| `gpt-5.6-terra` | OpenAIResponses | `https://api.openai.com/v1` | 272K | 128K | yes | `reasoning.effort` (high) |
| `gpt-5.6-luna` | OpenAIResponses | `https://api.openai.com/v1` | 272K | 128K | yes | `reasoning.effort` (high) |
| `gpt-5-chat-latest` | OpenAIResponses | `https://api.openai.com/v1` | 128K | — | yes | — |
| `gpt-5.5` | OpenAIResponses | `https://api.openai.com/v1` | 272K | — | yes | `reasoning.effort` (high) |
| `gpt-5.5-pro` | OpenAIResponses | `https://api.openai.com/v1` | 1.05M | — | yes | `reasoning.effort` (high) |
| `deepseek-flash` | OpenAI | `https://api.deepseek.com` | 1M | 384K | yes | `thinking` + `reasoning_effort` |
| `deepseek-v4-pro` | OpenAI | `https://api.deepseek.com` | 1M | 384K | no | same as Flash |
| `gemini-2.5-pro` | Gemini | Google Generative Language | 1M | — | yes | `thinkingBudget` (token cap) |
| `gemini-2.5-flash` | Gemini | Google Generative Language | 1M | — | yes | `thinkingBudget` |
| `gemini-3-pro` | Gemini | Google Generative Language | 1M | — | yes | `thinkingLevel` (off floor `LOW`) |
| `gemini-3-flash` | Gemini | Google Generative Language | 1M | — | yes | `thinkingLevel` (off floor `MINIMAL`) |
| `kimi-k3` | OpenAI | `https://api.moonshot.cn/v1` | 1M | 131K | yes | `reasoning_effort` (max) |
| `kimi-k2.7-code` | OpenAI | `https://api.moonshot.cn/v1` | 10M | — | yes | — |
| `glm-5.3` | OpenAI | `https://api.z.ai/api/coding/paas/v4` | 1M | 128K | no | endpoint-specific `thinking` + `reasoning_effort` |
| `glm-5.3-flash` | OpenAI | `https://api.z.ai/api/coding/paas/v4` | 1M | 128K | yes | same as `glm-5.3` |

Minimal config for a preset (api key only):

```yaml
models:
  - name: deepseek-flash
    api_key: sk-...
    default: true
```

## Recommended: DeepSeek Flash

`deepseek-flash` is the preset to use for long agent sessions: 1M context, image
input, and prefix caching that holds up as the transcript grows. Only `name` and
`api_key` are required.

Measured over 39 LLM rounds of one session — prompt **16k → 40k** — cache hit
**95–100%** (avg **98.7%**):

| Round | Prompt tokens | Cached tokens | Cache hit |
| ---: | ---: | ---: | ---: |
| 1 | 16,176 | 15,872 | **98.1%** |
| 10 | 20,163 | 20,096 | **99.7%** |
| 20 | 27,604 | 26,624 | **96.4%** |
| 30 | 35,245 | 35,072 | **99.5%** |
| 39 | 39,794 | 39,552 | **99.4%** |

```mermaid
xychart-beta
    title "Cache hit % (39 rounds)"
    x-axis [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39]
    y-axis "Hit %" 95 --> 100
    line [98.1, 98.8, 97.5, 98.1, 98.1, 98.8, 99.1, 99.9, 98.7, 99.7, 99.5, 99.5, 96.3, 99.4, 97.0, 99.7, 99.9, 99.3, 98.3, 96.4, 98.9, 98.7, 97.2, 99.8, 95.0, 99.7, 99.2, 98.9, 99.3, 99.5, 99.5, 99.4, 99.2, 98.6, 99.4, 99.6, 100.0, 97.8, 99.4]
```

## Output limit

`max_tokens` is the model's output limit in tokens. Compaction summaries use it
as their cap, clamped so the prompt plus output fits `context_window` (minus a
4096-token margin). Presets with a value in the **Output** column supply it;
other models use 16384 unless you set it:

```yaml
models:
  - name: my-gateway/some-reasoning-model
    api: OpenAI
    base_url: https://gateway.example/v1
    api_key: ...
    context_window: 1000000
    max_tokens: 128000
```

It currently sizes compaction summaries only; normal turns keep their existing
caps. It is a model setting, not a request override: `request_parameters`
still rejects `max_tokens` and other output-limit fields.

## Other / custom models

Any other `name` works as a normal entry. Set fields yourself:

```yaml
models:
  - name: gpt-5
    api: OpenAIResponses        # /v1/responses (not chat-completions)
    api_key: sk-...
    base_url: https://api.openai.com/v1
    context_window: 272000
    default: true

  - name: gpt-4o
    api: OpenAI                 # optional; default
    api_key: sk-...
    base_url: https://api.openai.com/v1
    context_window: 128000

  - name: claude-sonnet-4-20250514
    api: Anthropic              # required for Anthropic Messages API
    api_key: sk-ant-...
    base_url: https://api.anthropic.com
    context_window: 200000

  - name: my-proxy-model
    api: OpenAI
    api_key: ...
    base_url: https://proxy.example/v1
```

`OpenAIResponses` uses the Responses wire format (`input` items, typed SSE
events, flat function tools). Tool call IDs are stored as `call_id|item_id`
when the stream provides both, matching round-trip needs for
`function_call_output`.
Gemini without a preset name still needs `api: Gemini` and a valid base URL;
thinking then uses the budget style by default.

## Thinking level

Provider-agnostic knobs on each model (and session UI):

| Key / env | Meaning |
| --------- | ------- |
| `think_enabled` | bool; enable reasoning payload |
| `think_level` | `off` \| `minimal` \| `low` \| `medium` \| `high` \| `xhigh` \| `max` |
| `PHI_THINK_LEVEL` | env override on the default model (`off` disables) |

How that maps on the wire depends on the provider / preset interceptor
(OpenAI `reasoning_effort`, Anthropic thinking budget, Gemini budget or level).

GLM 5.3 and Kimi K3 support `low`, `high`, `max`; DeepSeek also supports `off`.
OpenCode Go Chat allows `low` / `high` / `max` for Flash and `high` / `max` for Pro.
Configuration loading rejects unsupported levels, including disabling required
thinking. Both pickers show supported levels; custom models use the generic list.

```yaml
models:
  - name: gemini-2.5-flash
    api_key: ...
    think_level: medium
```

## Request parameters

Conversations and compaction apply parameters in this order:
API baseline → model/endpoint adapter → `request_parameters`.

GLM and DeepSeek Chat use top-level `thinking` and `reasoning_effort`. GLM
always enables thinking; DeepSeek follows the toggle. GLM also sends
`clear_thinking: false` on Z.ai and BigModel HTTPS `/api/paas/v4` and
`/api/coding/paas/v4` endpoints to preserve reasoning history.
On `https://opencode.ai/zen/go/v1`, both use `reasoning_effort` alone.

GLM and DeepSeek Messages use budget-free `thinking` and `output_config.effort`.
On OpenRouter (`https://openrouter.ai/api` or `/api/v1`), enabled thinking uses
`adaptive`; disabled thinking uses `disabled`. Native Claude retains budget checks.

Endpoint matching normalizes host case, default ports and trailing slashes,
and accepts full `/chat/completions` URLs. Paths must match exactly; credentials,
query strings, fragments and non-default ports exclude native endpoint rules.

Each model can override options using the API's JSON field names:

```yaml
request_parameters:
  thinking:
    clear_thinking: null
```

Objects merge recursively; `null` deletes a field or object, while `false` and
`0` remain explicit. Overrides take precedence over session thinking controls.
The editor preserves these options but does not expose them as editable fields.

Model identity, messages, tools, streaming controls, choice count and output
limits remain managed by phi. Gemini options must merge into `generationConfig`
without replacing it or changing `maxOutputTokens` / `candidateCount` (including
snake_case aliases). Anthropic validates final thinking budgets. Vendor fields
belong directly under `request_parameters`, not an `extra_body` wrapper.

## Models from extensions

Extensions can add connections at runtime without touching config.yaml:

- **Direct-connect** — the host dials `base_url` with the registered
  `api`/`api_key`, same as a config entry.
- **Extension-served** (`Conn: ext`) — the extension proxies or generates
  every request itself and streams deltas back; see
  [extensions.md § Models](extensions.md#models-from-extensions).

Extension names shadow config entries with the same name; preset resolution,
thinking validation and context-window handling match config entries. The
`/model` palette snapshots names at startup, so restart once after installing
a model extension.

## Related

- Config overview: [README § Configuration](../README.md#configuration)
- Preset code: `internal/project/model/`
