import logging
import os
from logging.handlers import RotatingFileHandler
from pathlib import Path

_logger_initialized = False

def setup_error_logging(log_file='errors.log', max_bytes=10*1024*1024, backup_count=5):
    global _logger_initialized
    
    if _logger_initialized:
        return
    
    log_path = Path(log_file)
    log_path.parent.mkdir(parents=True, exist_ok=True)
    
    error_handler = RotatingFileHandler(
        log_file,
        maxBytes=max_bytes,
        backupCount=backup_count,
        encoding='utf-8'
    )
    error_handler.setLevel(logging.ERROR)
    
    error_format = logging.Formatter(
        '%(asctime)s - %(name)s - %(levelname)s - %(filename)s:%(lineno)d - %(message)s',
        datefmt='%Y-%m-%d %H:%M:%S'
    )
    error_handler.setFormatter(error_format)
    
    root_logger = logging.getLogger()
    root_logger.addHandler(error_handler)
    
    if root_logger.level == logging.NOTSET or root_logger.level > logging.INFO:
        root_logger.setLevel(logging.INFO)
    
    _logger_initialized = True
    logging.info(f"Error logging initialized: {log_file}")

def get_logger(name):
    return logging.getLogger(name)
