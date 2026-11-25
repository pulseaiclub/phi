function on_load()
  phi.provider.register({
    id = "acme",
    api = "OpenAI",
    base_url = "https://api.acme.example/v1",
    models = {
      { id = "chat", context_window = 128000, max_tokens = 8192 },
    },
    auth = phi.oauth2.pkce({
      client_id = "phi-cli",
      authorize_url = "https://auth.acme.example/authorize",
      token_url = "https://auth.acme.example/token",
      scopes = { "chat" },
    }),
  })
end
