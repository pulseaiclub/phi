# Supported models

phi talks to LLMs through an **explicit** `api` field on each config entry
(`OpenAI` | `OpenAIResponses` | `Anthropic` | `Gemini` | `OrcaRouter`). Empty
`api` means OpenAI-compatible `/chat/completions`. There is no name- or
URL-based provider guessing, with one deliberate exception: a model whose name
carries an OrcaRouter vendor namespace (`openai/…`, `anthropic/…`, `google/…`,
`deepseek/…`, `orcarouter/…`) routes to OrcaRouter, because that is the only
endpoint that accepts those IDs.

Built-in **presets** (exact `name` match) fill `base_url`, `context_window`,
`image_enabled`, `api`, and default thinking when those fields are omitted.
Source of truth: [`internal/project/model`](../internal/project/model/).

## Built-in presets

| Name | API | Base URL (default) | Context | Images | Thinking wire |
| ---- | --- | ------------------ | ------: | :----: | ------------- |
| `gpt-6-astra` | OpenAIResponses | `https://api.openai.com/v1` | 272K | yes | `reasoning.effort` (max) |
| `gpt-5.6-sol` | OpenAIResponses | `https://api.openai.com/v1` | 272K | yes | `reasoning.effort` (high) |
| `gpt-5.6-terra` | OpenAIResponses | `https://api.openai.com/v1` | 272K | yes | `reasoning.effort` (high) |
| `gpt-5.6-luna` | OpenAIResponses | `https://api.openai.com/v1` | 272K | yes | `reasoning.effort` (high) |
| `gpt-5-chat-latest` | OpenAIResponses | `https://api.openai.com/v1` | 128K | yes | — |
| `gpt-5.5` | OpenAIResponses | `https://api.openai.com/v1` | 272K | yes | `reasoning.effort` (high) |
| `gpt-5.5-pro` | OpenAIResponses | `https://api.openai.com/v1` | 1.05M | yes | `reasoning.effort` (high) |
| `deepseek-flash` | OpenAI | `https://api.deepseek.com` | 1M | yes | `extra_body.thinking` + `reasoning_effort` |
| `deepseek-v4-pro` | OpenAI | `https://api.deepseek.com` | 1M | no | same as Flash |
| `gemini-2.5-pro` | Gemini | Google Generative Language | 1M | yes | `thinkingBudget` (token cap) |
| `gemini-2.5-flash` | Gemini | Google Generative Language | 1M | yes | `thinkingBudget` |
| `gemini-3-pro` | Gemini | Google Generative Language | 1M | yes | `thinkingLevel` (off floor `LOW`) |
| `gemini-3-flash` | Gemini | Google Generative Language | 1M | yes | `thinkingLevel` (off floor `MINIMAL`) |
| `kimi-k3` | OpenAI | `https://api.moonshot.cn/v1` | 1M | yes | `reasoning_effort` (max) |
| `kimi-k2.7-code` | OpenAI | `https://api.moonshot.cn/v1` | 10M | yes | — |
| `glm-5.3` | OpenAI | `https://api.z.ai/api/coding/paas/v4` | 1M | no | `extra_body.thinking` + `reasoning_effort` |
| `glm-5.3-flash` | OpenAI | `https://api.z.ai/api/coding/paas/v4` | 1M | yes | same as `glm-5.3` |

Minimal config for a preset (api key only):

```yaml
models:
  - name: deepseek-flash
    api_key: sk-...
    default: true
```

## OrcaRouter

[OrcaRouter](https://www.orcarouter.ai) is a first-class provider: select it in
the config editor's `api` dropdown, or write `api: OrcaRouter`. It is an
OpenAI-compatible gateway, so the same client that talks to OpenAI-compatible
endpoints is used, with `base_url: https://api.orcarouter.ai/v1` filled in for
you.

### Connecting

Two ways in, and both end in the same OrcaRouter API key stored in
`~/.phi/orcarouter.json` (mode 0600):

```bash
phi auth login --orcarouter              # OAuth 2.0 + PKCE, no client secret
phi auth login --orcarouter --api-key    # paste an existing sk-orca-... key
phi auth status                          # masked key and its source
phi auth logout                          # remove the stored key
```

The config editor (`phi config`) shows both methods side by side in the
OrcaRouter section: a paste-a-key field, and **Connect with OrcaRouter**, which
runs the authorization-code flow with S256 PKCE. `ORCA_API_KEY` overrides the
stored key, matching the `PHI_API_KEY` habit.

The flow requests `scope=api`; a `connector` scope is also accepted when the
consent screen grants it. The granted scope is read from the exchange response
— the requested scope is never assumed.

### Model list

The model dropdown is filled from `GET /v1/models` on the configured
inference origin, using the stored key, and filtered per entry point:

| Entry point | Filter |
| ----------- | ------ |
| Text chat / agent | a chat-capable endpoint type (`openai`, `anthropic`, `gemini`, `openai-response`), excluding image-generation, video, and rerank routes |
| Image attachments | the chat list, narrowed to models whose `architecture.input_modalities` declares `image` |
| Embedding | the `embeddings` endpoint |
| Image generation | the `image-generation` endpoint |
| Video | the `openai-video` endpoint |
| Rerank | the `jina-rerank` endpoint |

A model that declares nothing is excluded rather than guessed at. Turning the
per-model image toggle on or off recomputes the list, and a selection that is
no longer compatible is cleared instead of being kept. If discovery fails, the
list falls back to a small verified seed and the page says so; the seed is
never merged into a successful live answer.

The API key stays in the local server: the browser receives model metadata
only.

```yaml
models:
  - name: openai/gpt-5.5
    api: OrcaRouter
    default: true
```

### Origins

Auth and inference are separate origins and are never derived from one another:
authentication is `https://www.orcarouter.ai` (authorize `/auth`, exchange
`/api/v1/auth/keys`) and inference is `https://api.orcarouter.ai/v1`. Note the
`/api` prefix on the exchange path — `/v1/auth/keys` on the inference origin
does not exist. Overrides: `ORCA_AUTH_BASE_URL`, `ORCA_API_BASE_URL`, or a
shared `ORCA_BASE_URL`. A non-loopback origin must be HTTPS.

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

```yaml
models:
  - name: gemini-2.5-flash
    api_key: ...
    think_level: medium
```

## Related

- Config overview: [README § Configuration](../README.md#configuration)
- Preset code: `internal/project/model/`
- OrcaRouter credential seam and catalog: `internal/orca/`
