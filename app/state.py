import json
import os
from typing import Any, Dict, Optional

STATE_FILE = "bot_state.json"


def load_state(path: str = STATE_FILE) -> Dict[str, Any]:
    try:
        if os.path.exists(path):
            with open(path, "r", encoding="utf-8") as f:
                data = json.load(f)
                if isinstance(data, dict):
                    return data
    except Exception:
        pass
    return {}


def save_state(data: Dict[str, Any], path: str = STATE_FILE) -> None:
    try:
        with open(path, "w", encoding="utf-8") as f:
            json.dump(data, f, ensure_ascii=False, indent=2)
    except Exception:
        pass


def get_last_chat_id() -> Optional[int]:
    st = load_state()
    cid = st.get("last_chat_id")
    try:
        return int(cid) if cid is not None else None
    except Exception:
        return None


def set_last_chat_id(chat_id: int) -> None:
    st = load_state()
    st["last_chat_id"] = int(chat_id)
    save_state(st)


def set_next_run_iso(dt_iso: str) -> None:
    st = load_state()
    st["next_run_iso"] = dt_iso
    save_state(st)


def get_next_run_iso() -> Optional[str]:
    st = load_state()
    v = st.get("next_run_iso")
    if isinstance(v, str) and v:
        return v
    return None
