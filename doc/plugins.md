# Lua plugins

`phi` and `phi run` load trusted local Lua plugins from `~/.phi/plugins/`.
Each plugin is a directory containing `plugin.yaml` and its Lua entry file.
For example, `~/.phi/plugins/hello/plugin.yaml`:

```yaml
name: hello
version: "0.1.0"
entry: init.lua
```

`name` defaults to the directory name, and `entry` to `init.lua`. The entry
must be a `.lua` file within the plugin directory. `version` is optional
metadata. Set `min_phi_version` (for example `"0.30.1"`) to skip the plugin
on older hosts.

In `~/.phi/plugins/hello/init.lua`:

```lua
function on_load()
  print("hello loaded")
end

function on_unload()
  print("hello unloaded")
end
```

Both hooks are optional. Each entry runs in a separate lug environment.
Directories load in filename order. An invalid manifest or Lua error is
reported to stderr without stopping other plugins. On normal shutdown,
successfully loaded plugins unload in reverse order. `print` and `io.write`
also go to stderr, not the headless JSONL stream. Restart phi after changes.

Project plugins under `<cwd>/.phi/plugins/` are **not** loaded by default.
For a trusted project, run `PHI_PLUGINS=project phi` to include them after
the user plugins for that invocation. Duplicate plugin names are skipped
with a warning. Use `PHI_PLUGINS=off` to skip both directories.

Plugins execute synchronously inside phi with lug's standard library. They
are not sandboxed or time-limited: a plugin can access process state, hang,
or terminate phi. Only install code you trust. There is no tool, command,
or permission registration API.
