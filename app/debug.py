import threading, time, os

_phase_lock = threading.Lock()
_current_phase = "idle"
_phase_ts = 0.0

def set_phase(name: str):
    global _current_phase, _phase_ts
    with _phase_lock:
        _current_phase = name
        _phase_ts = time.time()
        try:
            path = os.getenv('DEBUG_PHASE_FILE', 'phase.log')
            with open(path, 'a', encoding='utf-8') as f:
                f.write(f"{time.strftime('%Y-%m-%d %H:%M:%S')} {name}\n")
        except Exception:
            pass

def get_phase() -> str:
    with _phase_lock:
        return _current_phase
