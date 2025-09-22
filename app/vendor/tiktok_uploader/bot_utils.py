import requests, secrets, string, uuid, zlib, json, re, time, subprocess, os, shutil
from requests_auth_aws_sigv4 import AWSSigV4


user_agent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"


def subprocess_jsvmp(js, user_agent, url):
    def _run_node():
        try:
            print(f"[TikTok] Running node command: node {js} {url[:50]}... {user_agent[:50]}...")
            res = subprocess.run(['node', js, url, user_agent], capture_output=True, text=True, timeout=45)
            print(f"[TikTok] Node process completed with return code: {res.returncode}")
            if res.stderr:
                print(f"[TikTok] Node stderr: {res.stderr}")
            return res.returncode, res.stdout, res.stderr
        except subprocess.TimeoutExpired:
            print("[TikTok] Node process timed out after 45 seconds")
            return 124, '', 'Process timed out'
        except FileNotFoundError:
            print("[TikTok] Node executable not found in PATH")
            return 127, '', 'node executable not found in PATH'

    rc, out, err = _run_node()
    print(f"[TikTok] Node output length: {len(out) if out else 0}, error length: {len(err) if err else 0}")
    if rc == 0 and out:
        return out

    signer_dir = os.path.dirname(os.path.abspath(js))
    need_retry = False
    msg = (err or '').lower()
    if 'cannot find module' in msg and 'playwright-chromium' in msg:
        need_retry = True
        print("[TikTok] Missing playwright-chromium, will try auto-install")
    elif not out:
        need_retry = True
        print("[TikTok] No output from node, will try auto-install")

    if need_retry:
        try:
            npm_bin = shutil.which('npm') or shutil.which('npm.cmd') or shutil.which('npm.exe')
            npx_bin = shutil.which('npx') or shutil.which('npx.cmd') or shutil.which('npx.exe')
            npm_ok = npm_bin is not None
            if not npm_ok:
                print('[TikTok] npm not found; cannot auto-install tiktok-signature dependencies')
            else:
                print("[TikTok] Installing npm dependencies...")
                subprocess.run([npm_bin, 'install', '--no-audit', '--no-fund'], cwd=signer_dir, check=False)
                if npx_bin:
                    print("[TikTok] Installing playwright browsers...")
                    subprocess.run([npx_bin, 'playwright-chromium', 'install'], cwd=signer_dir, check=False)
                    subprocess.run([npx_bin, 'playwright', 'install', 'chromium'], cwd=signer_dir, check=False)
                else:
                    print('[TikTok] npx not found; skipping playwright browser install step')
        except Exception as _e:
            print(f"[TikTok] Auto-install failed: {_e}")
        print("[TikTok] Retrying node process after auto-install...")
        rc, out, err = _run_node()
        print(f"[TikTok] Retry result: rc={rc}, out_len={len(out) if out else 0}")
        if rc == 0 and out:
            return out
    
    # Try fallback signature generator if browser fails
    if not out:
        print("[TikTok] Browser signature failed, trying fallback generator...")
        try:
            fallback_path = os.path.join(signer_dir, 'fallback.js')
            if os.path.exists(fallback_path):
                res = subprocess.run(['node', fallback_path, url, user_agent], 
                                   capture_output=True, text=True, timeout=10)
                if res.returncode == 0 and res.stdout:
                    print("[TikTok] Fallback signature generated successfully")
                    return res.stdout
        except Exception as e:
            print(f"[TikTok] Fallback signature failed: {e}")
    
    print(f"[TikTok] Final result: returning {len(out) if out else 0} chars")
    return out


def generate_random_string(length, underline):
    characters = (
        string.ascii_letters + string.digits + "_"
        if underline
        else string.ascii_letters + string.digits
    )
    random_string = "".join(secrets.choice(characters) for _ in range(length))
    return random_string


def crc32(content):
    prev = 0
    prev = zlib.crc32(content, prev)
    return ("%X" % (prev & 0xFFFFFFFF)).lower().zfill(8)


def print_response(r):
    print(f"{r.status_code}")
    print(f"{r.content}")


def print_error(url, r):
    print(f"[-] An error occured while reaching {url}")
    print_response(r)


def assert_success(url, r):
    if r.status_code != 200:
        print_error(url, r)
    return r.status_code == 200


def convert_tags(text, session):
    end = 0
    i = -1
    text_extra = []

    def text_extra_block(start, end, type, hashtag_name, user_id, tag_id):
        return {
            "end": end,
            "hashtag_name": hashtag_name,
            "start": start,
            "tag_id": tag_id,
            "type": type,
            "user_id": user_id
        }

    def convert(match):
        nonlocal i, end, text_extra
        i += 1
        if match.group(1):
            text_extra.append(text_extra_block(end, end + len(match.group(1)) + 1, 1, match.group(1), "", str(i)))
            end += len(match.group(1)) + 1
            return "<h id=\"" + str(i) + "\">#" + match.group(1) + "</h>"
        elif match.group(2):
            url = "https://www.tiktok.com/@" + match.group(2)
            headers = {
                'authority': 'www.tiktok.com',
                'accept': '*/*',
                'accept-language': 'q=0.9,en-US;q=0.8,en;q=0.7,zh-CN;q=0.6,zh;q=0.5,vi;q=0.4',
                'user-agent': user_agent
            }

            r = session.request("GET", url, headers=headers)
            user_id = r.text.split('webapp.user-detail":{"userInfo":{"user":{"id":"')[1].split('"')[0]
            text_extra.append(text_extra_block(end, end + len(match.group(2)) + 1, 0, "", user_id, str(i)))
            end += len(match.group(2)) + 1
            return "<m id=\"" + str(i) + "\">@" + match.group(2) + "</m>"
        else:
            end += len(match.group(3))
            return match.group(3)

    result = re.sub(r'#(\w+)|@([\w.-]+)|([^#@]+)', convert, text)
    return result, text_extra


def printResponse(r):
    print(f"{r }")
    print(f"{r.content }")


def printError(url, r):
    print(f"[-] An error occured while reaching {url}")
    printResponse(r)


def assertSuccess(url, r):
    if r.status_code != 200:
        printError(url, r)
    return r.status_code == 200


def getTagsExtra(title, tags, users, session):
    text_extra = []
    for tag in tags:
        url = "https://www.tiktok.com/api/upload/challenge/sug/"
        params = {"keyword": tag}
        r = session.get(url, params=params)
        if not assertSuccess(url, r):
            return False
        try:
            verified_tag = r.json()["sug_list"][0]["cha_name"]
        except:
            verified_tag = tag
        title += " #"+verified_tag
        text_extra.append({"start": len(title)-len(verified_tag)-1, "end": len(
            title), "user_id": "", "type": 1, "hashtag_name": verified_tag})
    for user in users:
        url = "https://us.tiktok.com/api/upload/search/user/"
        params = {"keyword": user}
        r = session.get(url, params=params)
        if not assertSuccess(url, r):
            return False
        try:
            verified_user = r.json()["user_list"][0]["user_info"]["unique_id"]
            verified_user_id = r.json()["user_list"][0]["user_info"]["uid"]
        except:
            verified_user = user
            verified_user_id = ""
        title += " @"+verified_user
        text_extra.append({"start": len(title)-len(verified_user)-1, "end": len(
            title), "user_id": verified_user_id, "type": 0, "hashtag_name": verified_user})
    return title, text_extra
