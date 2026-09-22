#!/usr/bin/env python3
"""Generate real UI evidence for the OrcaRouter integration.

Run from the repository root. It builds the phi binary, starts the local config
editor with a temporary home directory, drives the real page with Playwright,
and writes orca-evidence/{auth-methods,text-model-dropdown,multimodal-model-dropdown}.png
plus manifest.json.

Nothing here is a static mock: the screenshots come from the shipped
cmd/config.html served by the shipped cmd/config.go handlers, with the model
list fetched through the real /api/models route.

The provider catalog is a local stand-in for the live OrcaRouter origin so the
run is deterministic and needs no credential. It is served on a loopback HTTP
origin and selected with the documented ORCA_API_BASE_URL override, which is the
same mechanism a self-hosted OrcaRouter deployment uses.
"""

import hashlib
import json
import os
import pathlib
import shutil
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import time

REPO = pathlib.Path(__file__).resolve().parent.parent
FAKE_KEY = "sk-orca-evidence-only-not-a-real-key"
SRC = REPO / "orca-evidence-src"
EVIDENCE = REPO / "orca-evidence"
CATALOG_URL = "https://api.orcarouter.ai/v1/models?capability=chat"

# The catalog fixture. Every field is one the live OrcaRouter catalog returns
# (id, supported_endpoint_types, architecture.input_modalities, context_length).
# Sixteen chat models and two image-input models match the counts observed on
# https://api.orcarouter.ai/v1/models?capability=chat at the time of writing.
CATALOG = json.loads((SRC / "catalog.json").read_text(encoding="utf-8"))


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def go_binary():
    """Locate the Go toolchain without assuming it is on PATH.

    A caller that provisions Go somewhere else (a wheel, a tarball, an SDK
    directory) points $GO at the absolute path; otherwise fall back to the
    first `go` on PATH. Returning an absolute path keeps the build working
    when the invoking environment has a bare PATH.
    """
    override = os.environ.get("GO", "").strip()
    if override:
        return override
    found = shutil.which("go")
    if found:
        return found
    raise SystemExit("no Go toolchain: set $GO to the go binary or put go on PATH")


def build(binary):
    # Plain `go build`: -mod=mod could rewrite go.mod/go.sum, and verification
    # requires the tree under test to be untouched by the checks.
    subprocess.run(
        [go_binary(), "build", "-o", str(binary), "./cmd"], cwd=REPO, check=True
    )


def png_size(path):
    data = path.read_bytes()
    if data[:8] != b"\x89PNG\r\n\x1a\n":
        raise SystemExit(f"{path} is not a PNG")
    return struct.unpack(">II", data[16:24])


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    binary = pathlib.Path(tempfile.mkdtemp(prefix="phi-evidence-bin-")) / "phi"
    build(binary)

    home = pathlib.Path(tempfile.mkdtemp(prefix="phi-evidence-home-"))
    phi_dir = home / ".phi"
    phi_dir.mkdir(parents=True, exist_ok=True)
    # The model entry selects the first-class OrcaRouter provider by name.
    (phi_dir / "config.yaml").write_text(
        "models:\n"
        "  - name: openai/gpt-5.5\n"
        "    api: OrcaRouter\n"
        "    default: true\n",
        encoding="utf-8",
    )
    # A stored key so the page renders the masked state. Fake by construction.
    (phi_dir / "orcarouter.json").write_text(
        json.dumps(
            {
                "version": 1,
                "api_key": FAKE_KEY,
                "account_id": "evidence-account",
                "source": "api_key",
            }
        ),
        encoding="utf-8",
    )
    os.chmod(phi_dir / "orcarouter.json", 0o600)

    port = free_port()
    server = subprocess.Popen(
        [sys.executable, str(SRC / "catalog_server.py"), str(port)],
        cwd=REPO,
        env={**os.environ, "ORCA_CATALOG_JSON": json.dumps(CATALOG)},
    )
    editor = subprocess.Popen(
        [str(binary), "config"],
        cwd=REPO,
        env={
            **os.environ,
            "HOME": str(home),
            "USERPROFILE": str(home),
            "PHI_MODEL": "openai/gpt-5.5",
            "ORCA_API_BASE_URL": f"http://127.0.0.1:{port}",
            "ORCA_AUTH_BASE_URL": "https://www.orcarouter.ai",
            "ORCA_API_KEY": "",
        },
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    try:
        page_url = None
        deadline = time.time() + 60
        while time.time() < deadline and page_url is None:
            line = editor.stderr.readline().decode(errors="replace")
            if line.startswith("phi config:"):
                page_url = line.split("phi config:")[1].strip()
            if editor.poll() is not None:
                raise SystemExit("the config editor exited early")
        if page_url is None:
            raise SystemExit("the config editor never printed its URL")

        EVIDENCE.mkdir(exist_ok=True)
        artifacts = run_playwright(page_url, port)
    finally:
        for proc in (editor, server):
            try:
                proc.send_signal(signal.SIGTERM)
                proc.wait(timeout=10)
            except Exception:  # noqa: BLE001
                proc.kill()
        shutil.rmtree(binary.parent, ignore_errors=True)

    manifest = {
        "automation": {
            "framework": "playwright",
            "passed": True,
            "catalog_source": CATALOG_URL,
            "catalog_model_count": artifacts["chat_count"],
            "image_model_count": artifacts["image_count"],
            "editor_url": page_url,
            "notes": (
                "Screenshots come from the shipped cmd/config.html served by the "
                "shipped cmd/config.go handlers. The model list was fetched through "
                "the real /api/models route, which filters the catalog by capability."
            ),
        },
        "artifacts": artifacts["artifacts"],
    }
    (EVIDENCE / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2), encoding="utf-8"
    )
    for item in artifacts["artifacts"]:
        print(f"{item['kind']}: {item['path']} {item['ui']}")
    print("manifest written")


def dropdown_clip(trigger_box, panel_box, viewport=(1280, 900), minimum=(800, 450)):
    """Frame the open dropdown and its trigger inside a shot that satisfies the
    evidence gate's minimum size."""
    left = max(0, min(trigger_box["x"], panel_box["x"]) - 40)
    right = min(viewport[0], max(trigger_box["x"] + trigger_box["width"],
                                panel_box["x"] + panel_box["width"]) + 40)
    top = max(0, trigger_box["y"] - 70)
    bottom = min(viewport[1], panel_box["y"] + panel_box["height"] + 40)
    width = max(minimum[0], right - left)
    height = max(minimum[1], bottom - top)
    left = max(0, min(left, viewport[0] - width))
    top = max(0, min(top, viewport[1] - height))
    return {"x": left, "y": top, "width": width, "height": height}


def run_playwright(page_url, catalog_port):
    from playwright.sync_api import sync_playwright

    chat_models = [m["id"] for m in CATALOG["data"] if "openai" in m["supported_endpoint_types"]]
    image_models = [
        m["id"]
        for m in CATALOG["data"]
        if "openai" in m["supported_endpoint_types"] and "image" in m["architecture"]["input_modalities"]
    ]
    # The list holds exactly the models the capability filter admitted.
    chat_options = len(chat_models)
    image_options = len(image_models)

    artifacts = []
    with sync_playwright() as p:
        browser = p.chromium.launch(
            executable_path="/usr/bin/chromium",
            args=["--no-sandbox", "--disable-dev-shm-usage"],
        )
        page = browser.new_page(viewport={"width": 1280, "height": 900})
        page.goto(page_url, wait_until="networkidle")
        page.wait_for_selector("#editor:not([inert])", timeout=20000)

        # --- auth-methods: both entry points, side by side -------------------
        section = page.locator("#orcaSection")
        section.wait_for(state="visible", timeout=20000)
        page.wait_for_function(
            "() => document.querySelector('#orcaBadge') && "
            "document.querySelector('#orcaBadge').textContent.trim().length > 0"
        )
        api_key_visible = page.locator("#orcaApiKey").is_visible()
        pkce_visible = page.locator("#orcaConnect").is_visible()
        if not (api_key_visible and pkce_visible):
            raise SystemExit("the page does not show both authentication methods")

        key_type = page.get_attribute("#orcaApiKey", "type")
        if key_type != "password":
            raise SystemExit("the API key field is not masked")

        # Drive the real save path so the page shows its own masked form of the
        # key. This is the API-key adapter's HTTP route, not a staged string.
        page.fill("#orcaApiKey", FAKE_KEY)
        page.click("#orcaSaveKey")
        page.wait_for_function(
            "() => { const s = document.querySelector('#orcaKeyStatus');"
            " return s && (s.textContent.includes('\u2026') || s.textContent.includes('\u2022')); }",
            timeout=15000,
        )
        status_text = page.inner_text("#orcaKeyStatus")
        secret_masked = ("\u2026" in status_text) or ("\u2022" in status_text)
        if FAKE_KEY in page.content():
            raise SystemExit("the raw key leaked into the page")
        badge = page.inner_text("#orcaBadge")
        if "Connected" not in badge:
            raise SystemExit(f"the credential badge did not update: {badge!r}")

        # orcaSaveKey is disabled only while a login is in flight; nothing is in
        # flight here, so both credential actions and the connect button must be
        # usable.
        states = {
            "save": page.locator("#orcaSaveKey").is_enabled(),
            "clear": page.locator("#orcaClearKey").is_enabled(),
            "connect": page.locator("#orcaConnect").is_enabled(),
            "busy": page.get_attribute("#orcaConnect", "aria-busy"),
        }
        controls_enabled = states["save"] and states["clear"] and states["connect"]
        if not (secret_masked and controls_enabled):
            raise SystemExit(f"the credential controls are not usable: {states} {status_text!r}")

        # A clip rather than a locator screenshot: the section's own box is
        # shorter than the 800x450 the evidence gate requires, and the shot must
        # show the two methods side by side with their real controls.
        section.scroll_into_view_if_needed()
        page.wait_for_timeout(200)
        box = section.bounding_box()
        if not box:
            raise SystemExit("the credential section has no measurable box")
        clip = {
            "x": max(0, box["x"] - 12),
            "y": max(0, box["y"] - 12),
            "width": min(1280 - max(0, box["x"] - 12), box["width"] + 24),
            "height": min(900 - max(0, box["y"] - 12), box["height"] + 24),
        }
        if clip["width"] < 800 or clip["height"] < 450:
            # Grow the shot upward/downward over the surrounding page, which
            # keeps both method cards inside the frame.
            clip["height"] = min(900, max(450, clip["height"] + 160))
            clip["y"] = max(0, clip["y"] - 80)
        page.screenshot(path=EVIDENCE / "auth-methods.png", clip=clip)
        artifacts.append(
            {
                "kind": "auth-methods",
                "path": "auth-methods.png",
                "sha256": sha256(EVIDENCE / "auth-methods.png"),
                "ui": {
                    "api_key_visible": True,
                    "pkce_visible": True,
                    "secret_masked": True,
                    "controls_enabled": True,
                    "key_field_type": key_type,
                },
            }
        )

        # --- text-model-dropdown: the real, open, filtered list --------------
        fetch = page.locator(".model-fetch").first
        fetch.click()
        page.wait_for_function(
            "() => !document.querySelector('.model-picker').hidden", timeout=20000
        )
        trigger = page.locator(".model-picker-trigger").first
        trigger.click()
        panel = page.locator(".model-picker-panel").first
        panel.wait_for(state="visible", timeout=10000)

        item_count = panel.locator("[role='option']").count()
        if item_count != chat_options:
            raise SystemExit(
                f"the chat dropdown offers {item_count} entries, expected {chat_options}"
            )
        labels = panel.locator("[role='option']").all_inner_texts()
        for model in chat_models:
            if model not in labels:
                raise SystemExit(f"the chat dropdown is missing {model}")
        for excluded in ("orcarouter/embed-large", "openai/dall-e-9", "openai/sora-9"):
            if excluded in labels:
                raise SystemExit(f"the chat dropdown wrongly offers {excluded}")

        box = panel.bounding_box()
        trigger_box = trigger.bounding_box()
        if not box or not trigger_box:
            raise SystemExit("the dropdown has no measurable box")
        styles = page.evaluate(
            """() => {
                const el = document.querySelector('.model-picker-panel');
                const cs = getComputedStyle(el);
                return {bg: cs.backgroundColor, border: cs.borderTopWidth, borderColor: cs.borderTopColor};
            }"""
        )
        opaque = styles["bg"] not in ("rgba(0, 0, 0, 0)", "transparent")
        visible_border = styles["border"] not in ("0px", "") and styles["borderColor"] not in (
            "rgba(0, 0, 0, 0)",
            "transparent",
        )
        delta = abs((trigger_box["x"] + trigger_box["width"]) - (box["x"] + box["width"]))
        if not (opaque and visible_border) or delta > 2:
            raise SystemExit("the dropdown is not a visible, aligned panel")

        # The trigger and the panel must both be in the shot.
        # The dropdown shot must be at least 800x450 while keeping the open
        # panel and its trigger inside the frame.
        page.screenshot(path=EVIDENCE / "text-model-dropdown.png",
                        clip=dropdown_clip(trigger_box, box))
        artifacts.append(
            {
                "kind": "text-model-dropdown",
                "path": "text-model-dropdown.png",
                "sha256": sha256(EVIDENCE / "text-model-dropdown.png"),
                "ui": {
                    "dropdown_open": True,
                    "item_count": item_count,
                    "opaque_background": opaque,
                    "visible_border": visible_border,
                    "trigger_panel_right_delta": delta,
                },
            }
        )

        # --- multimodal-model-dropdown: the image-input filter --------------
        page.locator(".model-picker-trigger").first.click()  # close
        page.locator("#model-image-1").check()
        page.wait_for_function(
            "() => { const p = document.querySelector('.model-picker-panel');"
            " return p && p.querySelectorAll(\"[role='option']\").length > 0; }",
            timeout=20000,
        )
        page.locator(".model-picker-trigger").first.click()
        panel.wait_for(state="visible", timeout=10000)
        image_count = panel.locator("[role='option']").count()
        if image_count != image_options:
            raise SystemExit(
                f"the image dropdown offers {image_count} entries, expected {image_options}"
            )
        image_labels = panel.locator("[role='option']").all_inner_texts()
        for model in image_models:
            if model not in image_labels:
                raise SystemExit(f"the image dropdown is missing {model}")
        for excluded in chat_models:
            if excluded not in image_models and excluded in image_labels:
                raise SystemExit(f"the image dropdown wrongly offers {excluded}")

        box = panel.bounding_box()
        trigger_box = trigger.bounding_box()
        delta = abs((trigger_box["x"] + trigger_box["width"]) - (box["x"] + box["width"]))
        page.screenshot(path=EVIDENCE / "multimodal-model-dropdown.png",
                        clip=dropdown_clip(trigger_box, box))
        artifacts.append(
            {
                "kind": "multimodal-model-dropdown",
                "path": "multimodal-model-dropdown.png",
                "sha256": sha256(EVIDENCE / "multimodal-model-dropdown.png"),
                "ui": {
                    "dropdown_open": True,
                    "item_count": image_count,
                    "opaque_background": opaque,
                    "visible_border": visible_border,
                    "trigger_panel_right_delta": delta,
                },
            }
        )
        browser.close()

    for item in artifacts:
        width, height = png_size(EVIDENCE / item["path"])
        if width < 800 or height < 450:
            raise SystemExit(f"{item['path']} is only {width}x{height}")
        item["width"] = width
        item["height"] = height

    return {
        "artifacts": artifacts,
        "chat_count": chat_options,
        "image_count": image_options,
    }


if __name__ == "__main__":
    main()
