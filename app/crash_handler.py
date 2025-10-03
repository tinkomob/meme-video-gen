import os
import json
import logging
import psutil
import threading
from datetime import datetime
from typing import Optional

GENERATION_STATE_FILE = "generation_state.json"
CRASH_LOG_FILE = "crash.log"
PHASE_LOG_FILE = os.getenv('DEBUG_PHASE_FILE', 'phase.log')

def save_generation_state(state: str, details: dict):
    try:
        with open(GENERATION_STATE_FILE, 'w', encoding='utf-8') as f:
            json.dump({
                "state": state, 
                "details": details, 
                "timestamp": datetime.now().isoformat()
            }, f, ensure_ascii=False, indent=2)
    except Exception as e:
        logging.error(f"Не удалось сохранить состояние генерации: {e}")

def load_last_crash_info() -> Optional[dict]:
    try:
        if os.path.exists(GENERATION_STATE_FILE):
            with open(GENERATION_STATE_FILE, 'r', encoding='utf-8') as f:
                data = json.load(f)
                state = data.get('state', '')
                error_states = ['generation_error', 'batch_error', 'init_error', 'dedup_error', 'results_error', 'send_error']
                if state in error_states:
                    return {
                        'state': state,
                        'error': data.get('details', {}).get('error', 'Неизвестная ошибка'),
                        'time': data.get('timestamp', 'неизвестно'),
                        'details': data.get('details', {})
                    }
    except Exception as e:
        logging.error(f"Не удалось загрузить информацию о крахе: {e}")
    return None

def clear_crash_info():
    try:
        if os.path.exists(GENERATION_STATE_FILE):
            os.remove(GENERATION_STATE_FILE)
    except Exception as e:
        logging.error(f"Не удалось очистить информацию о крахе: {e}")

def get_last_uncaught_exception() -> Optional[dict]:
    try:
        if not os.path.exists(CRASH_LOG_FILE):
            return None
        with open(CRASH_LOG_FILE, 'r', encoding='utf-8', errors='ignore') as f:
            lines = f.readlines()
        marker = "=== Uncaught Exception ==="
        last_idx = None
        for i in range(len(lines)-1, -1, -1):
            if marker in lines[i]:
                last_idx = i
                break
        if last_idx is None:
            return None
        block = [l.rstrip("\n") for l in lines[last_idx:]]
        summary = None
        for l in reversed(block):
            if l.strip():
                summary = l.strip()
                break
        return {"summary": summary or "", "block": block[-20:]}
    except Exception as e:
        logging.error(f"Не удалось прочитать crash.log: {e}")
        return None

def get_recent_phases(max_lines: int = 20) -> list[str]:
    try:
        if not os.path.exists(PHASE_LOG_FILE):
            return []
        with open(PHASE_LOG_FILE, 'r', encoding='utf-8', errors='ignore') as f:
            lines = [l.rstrip("\n") for l in f.readlines()[-max_lines:]]
        return lines
    except Exception:
        return []

def get_memory_info() -> dict:
    try:
        process = psutil.Process(os.getpid())
        mem = process.memory_info()
        return {
            'rss_mb': round(mem.rss / 1024 / 1024, 2),
            'vms_mb': round(mem.vms / 1024 / 1024, 2),
            'percent': round(process.memory_percent(), 2),
            'threads': process.num_threads()
        }
    except Exception as e:
        logging.error(f"Ошибка получения информации о памяти: {e}")
        return {}

def log_resource_usage(context: str = ""):
    try:
        mem_info = get_memory_info()
        msg = f"[{context}] Память: {mem_info.get('rss_mb', 0)}MB RSS, {mem_info.get('vms_mb', 0)}MB VMS, {mem_info.get('percent', 0)}%, потоки: {mem_info.get('threads', 0)}"
        logging.info(msg)
        return mem_info
    except Exception as e:
        logging.error(f"Ошибка логирования ресурсов: {e}")
        return {}
