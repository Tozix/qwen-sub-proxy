#!/usr/bin/env python3
"""Добавляет провайдер qwen-sub-proxy в ~/.zcode/v2/provider_config.json.

Использование: scripts/zcode-provider.py [--apply]
Без --apply только печатает, что было бы сделано.
"""
import json, os, shutil, sys, time

CONFIG = os.path.expanduser("~/.zcode/v2/provider_config.json")
KEY_FILE = os.path.expanduser("~/.config/qwen-sub-proxy/client-key")
PROVIDER_ID = "qwen-sub-proxy"
PROVIDER_NAME = "Qwen (chat.qwen.ai subscription)"
BASE_URL = "http://127.0.0.1:8791/v1"
MODELS = ["qwen3.8-max", "qwen3.8-flash", "qwen4.0-max-preview"]

def main() -> int:
    apply = "--apply" in sys.argv
    with open(CONFIG) as fh:
        data = json.load(fh)
    config = data["config"]

    with open(KEY_FILE) as fh:
        local_key = fh.read().strip()
    if not local_key:
        print("local key is empty, run `qwen-sub-proxy login` first", file=sys.stderr)
        return 1

    rules = config["providerConfigRules"]["providerRules"]
    rules = [r for r in rules if r.get("providerId") != PROVIDER_ID]
    rules.append({
        "providerId": PROVIDER_ID,
        "providerName": PROVIDER_NAME,
        "config": {
            "group": "standard-personal",
            "access": {"type": "api-key", "apiKey": local_key},
            "api": {"type": "openai-chat-completions", "baseUrl": BASE_URL},
            "personalModelIds": MODELS,
            "modelOrder": MODELS,
        },
    })
    config["providerConfigRules"]["providerRules"] = rules

    order = config.get("providerOrder", [])
    if PROVIDER_ID not in order:
        order.append(PROVIDER_ID)
    config["providerOrder"] = order

    model_rules = config["modelConfigRules"]["providerModelRules"]
    model_rules = [r for r in model_rules if r.get("providerId") != PROVIDER_ID]
    for model_id in MODELS:
        model_rules.append({
            "modelId": model_id,
            "config": {
                "enabled": True,
                "properties": {
                    "contextWindow": 1000000,
                    "inputFormat": {"supportsImage": False, "supportsVideo": False, "supportsPdf": False},
                },
                "optionSpecs": {"maxOutputTokens": {"max": 32768}},
            },
            "providerId": PROVIDER_ID,
        })
    config["modelConfigRules"]["providerModelRules"] = model_rules

    if not apply:
        print("dry run: would add provider", PROVIDER_ID, "to", CONFIG)
        return 0

    backup = f"{CONFIG}.bak-qwen-sub-proxy-{time.strftime('%Y%m%d-%H%M%S')}"
    shutil.copy2(CONFIG, backup)
    tmp = CONFIG + ".tmp"
    with open(tmp, "w") as fh:
        json.dump(data, fh, indent=2, ensure_ascii=False)
        fh.write("\n")
    os.chmod(tmp, 0o600)
    os.replace(tmp, CONFIG)
    print("patched", CONFIG)
    print("backup ", backup)
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
