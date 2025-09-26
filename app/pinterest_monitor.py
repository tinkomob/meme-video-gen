import time
import requests
import json
import os
from pathlib import Path

class PinterestMonitor:
    def __init__(self, config_file='pinterest_status.json'):
        self.config_file = config_file
        self.status = self.load_status()
    
    def load_status(self):
        """Load Pinterest status from file"""
        try:
            if os.path.exists(self.config_file):
                with open(self.config_file, 'r') as f:
                    return json.load(f)
        except Exception:
            pass
        
        return {
            'last_check': 0,
            'is_blocked': False,
            'consecutive_failures': 0,
            'last_success': time.time(),
            'fallback_mode': False,
            'recovery_attempts': 0
        }
    
    def save_status(self):
        """Save Pinterest status to file"""
        try:
            with open(self.config_file, 'w') as f:
                json.dump(self.status, f, indent=2)
        except Exception as e:
            print(f"Failed to save Pinterest status: {e}")
    
    def check_pinterest_availability(self):
        """Check if Pinterest is currently accessible"""
        try:
            headers = {
                'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36'
            }
            
            response = requests.get(
                'https://www.pinterest.com',
                headers=headers,
                timeout=10,
                allow_redirects=True
            )
            
            # Check for common blocking indicators
            if response.status_code >= 400:
                return False
            
            # Check for CAPTCHA or blocking pages
            content = response.text.lower()
            blocking_indicators = [
                'captcha',
                'blocked',
                'access denied',
                'rate limit',
                'try again later'
            ]
            
            if any(indicator in content for indicator in blocking_indicators):
                return False
                
            return True
            
        except Exception:
            return False
    
    def update_status(self, is_available):
        """Update Pinterest status based on availability check"""
        current_time = time.time()
        self.status['last_check'] = current_time
        
        if is_available:
            self.status['is_blocked'] = False
            self.status['consecutive_failures'] = 0
            self.status['last_success'] = current_time
            self.status['recovery_attempts'] = 0
            
            # Exit fallback mode if we were in it
            if self.status['fallback_mode']:
                print("Pinterest recovered! Exiting fallback mode.")
                self.status['fallback_mode'] = False
        else:
            self.status['is_blocked'] = True
            self.status['consecutive_failures'] += 1
            
            # Enter fallback mode after 3 consecutive failures
            if self.status['consecutive_failures'] >= 3:
                if not self.status['fallback_mode']:
                    print("Pinterest appears blocked. Entering fallback mode.")
                self.status['fallback_mode'] = True
        
        self.save_status()
    
    def should_use_fallback(self):
        """Determine if we should use fallback content instead of Pinterest"""
        current_time = time.time()
        
        # Always check if we haven't checked recently
        if current_time - self.status['last_check'] > 300:  # 5 minutes
            is_available = self.check_pinterest_availability()
            self.update_status(is_available)
        
        return self.status['fallback_mode']
    
    def get_status_info(self):
        """Get current status information"""
        current_time = time.time()
        last_check_ago = int(current_time - self.status['last_check'])
        last_success_ago = int(current_time - self.status['last_success'])
        
        return {
            'blocked': self.status['is_blocked'],
            'fallback_mode': self.status['fallback_mode'],
            'consecutive_failures': self.status['consecutive_failures'],
            'last_check_seconds_ago': last_check_ago,
            'last_success_seconds_ago': last_success_ago,
            'recovery_attempts': self.status['recovery_attempts']
        }
    
    def force_check(self):
        """Force an immediate Pinterest availability check"""
        is_available = self.check_pinterest_availability()
        self.update_status(is_available)
        return is_available

# Global monitor instance
_monitor = None

def get_pinterest_monitor():
    """Get the global Pinterest monitor instance"""
    global _monitor
    if _monitor is None:
        _monitor = PinterestMonitor()
    return _monitor

def should_use_pinterest_fallback():
    """Quick check if we should use fallback instead of Pinterest"""
    monitor = get_pinterest_monitor()
    return monitor.should_use_fallback()

def get_pinterest_status():
    """Get current Pinterest status"""
    monitor = get_pinterest_monitor()
    return monitor.get_status_info()