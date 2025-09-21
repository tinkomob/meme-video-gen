import time, requests, datetime, hashlib, hmac, random, zlib, json, datetime
import requests, zlib, json, time, subprocess, string, secrets, os, sys
from fake_useragent import FakeUserAgentError, UserAgent
from requests_auth_aws_sigv4 import AWSSigV4
from .cookies import load_cookies_from_file
from .Browser import Browser
from .bot_utils import *
from . import Config, Video, eprint
from dotenv import load_dotenv


load_dotenv()

_UA = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/68.0.3440.106 Safari/537.36'
_TIMEOUT = int(os.getenv('TIKTOK_REQUEST_TIMEOUT', '30'))


def login(login_name: str):
	cookies = load_cookies_from_file(f"tiktok_session-{login_name}")
	session_cookie = next((c for c in cookies if c["name"] == 'sessionid'), None)
	session_from_file = session_cookie is not None

	if session_from_file:
		print("Unnecessary login: session already saved!")
		return session_cookie["value"]

	browser = Browser.get()
	response = browser.driver.get(os.getenv("TIKTOK_LOGIN_URL"))

	session_cookies = []
	while not session_cookies:
		for cookie in browser.driver.get_cookies():
			if cookie["name"] in ["sessionid", "tt-target-idc"]:
				if cookie["name"] == "sessionid":
					cookie_name = cookie
				session_cookies.append(cookie)

	print("Account successfully saved.")
	browser.save_cookies(f"tiktok_session-{login_name}", session_cookies)
	browser.driver.quit()

	return cookie_name.get('value', '') if cookie_name else ''


def upload_video(session_user, video, title, schedule_time=0, allow_comment=1, allow_duet=0, allow_stitch=0, visibility_type=0, brand_organic_type=0, branded_content_type=0, ai_label=0, proxy=None):
	try:
		user_agent = UserAgent().random
	except FakeUserAgentError as e:
		user_agent = _UA
		print("[-] Could not get random user agent, using default")

	cookies = load_cookies_from_file(f"tiktok_session-{session_user}")
	session_id = next((c["value"] for c in cookies if c["name"] == 'sessionid'), None)
	dc_id = next((c["value"] for c in cookies if c["name"] == 'tt-target-idc'), None)
	
	if not session_id:
		eprint("No cookie with Tiktok session id found: use login to save session id")
		return {"success": False, "error": "missing_sessionid_cookie"}
	if not dc_id:
		print("[WARNING]: Please login, tiktok datacenter id must be allocated, or may fail")
		dc_id = "useast2a"
	print("User successfully logged in.")
	print(f"Tiktok Datacenter Assigned: {dc_id}")
	
	print("Uploading video...")
	if schedule_time and (schedule_time > 864000 or schedule_time < 900):
		print("[-] Cannot schedule video in more than 10 days or less than 20 minutes")
		return False
	if len(title) > 2200:
		print("[-] The title has to be less than 2200 characters")
		return False
	if schedule_time != 0 and visibility_type == 1:
		print("[-] Private videos cannot be uploaded with schedule")
		return False

	session = requests.Session()
	session.cookies.set("sessionid", session_id, domain=".tiktok.com")
	session.cookies.set("tt-target-idc", dc_id, domain=".tiktok.com")
	session.verify = True

	headers = {
		'User-Agent': user_agent,
		'Accept': 'application/json, text/plain, */*',
	}
	session.headers.update(headers)

	if proxy:
		session.proxies = {
			"http": proxy,
			"https": proxy
		}

	creation_id = generate_random_string(21, True)
	project_url = f"https://www.tiktok.com/api/v1/web/project/create/?creation_id={creation_id}&type=1&aid=1988"
	print(f"[TikTok] Creating project: {project_url}")
	r = session.post(project_url, timeout=_TIMEOUT)

	if not assert_success(project_url, r):
		return False

	project_id = r.json()["project"]["project_id"]
	up = upload_to_tiktok(video, session)
	if not up:
		print("[-] Upload init failed during ApplyUploadInner/part upload")
		return False
	video_id, session_key, upload_id, crcs, upload_host, store_uri, video_auth, aws_auth = up

	url = f"https://{upload_host}/{store_uri}?uploadID={upload_id}&phase=finish&uploadmode=part"
	headers = {
		"Authorization": video_auth,
		"Content-Type": "text/plain;charset=UTF-8",
	}
	data = ",".join([f"{i + 1}:{crcs[i]}" for i in range(len(crcs))])

	if proxy:
		r = requests.post(url, headers=headers, data=data, proxies=session.proxies)
		if not assert_success(url, r):
			return False
	else:
		r = requests.post(url, headers=headers, data=data)
		if not assert_success(url, r):
			return False

	url = f"https://www.tiktok.com/top/v1?Action=CommitUploadInner&Version=2020-11-19&SpaceName=tiktok"
	data = '{"SessionKey":"' + session_key + '","Functions":[{"name":"GetMeta"}]}'

	print("[TikTok] CommitUploadInner GetMeta")
	r = session.post(url, auth=aws_auth, data=data, timeout=_TIMEOUT)
	if not assert_success(url, r):
		return False

	print("[TikTok] Skipping homepage request - proceeding to signature generation")
	# Skip homepage request that often hangs - we already have session cookies set

	print("[TikTok] Preparing headers and data payload")
	headers = {
		"content-type": "application/json",
		"user-agent": user_agent
	}
	brand = ""

	if brand and brand[-1] == ",":
		brand = brand[:-1]
	
	print("[TikTok] Converting tags in title")
	markup_text, text_extra = convert_tags(title, session)
	print(f"[TikTok] Tag conversion complete, text_extra count: {len(text_extra)}")

	data = {
		"post_common_info": {
			"creation_id": creation_id,
			"enter_post_page_from": 1,
			"post_type": 3
		},
		"feature_common_info_list": [
			{
				"geofencing_regions": [],
				"playlist_name": "",
				"playlist_id": "",
				"tcm_params": "{\"commerce_toggle_info\":{}}",
				"sound_exemption": 0,
				"anchors": [],
				"vedit_common_info": {
					"draft": "",
					"video_id": video_id
				},
				"privacy_setting_info": {
					"visibility_type": visibility_type,
					"allow_duet": allow_duet,
					"allow_stitch": allow_stitch,
					"allow_comment": allow_comment
				}
			}
		],
		"single_post_req_list": [
			{
				"batch_index": 0,
				"video_id": video_id,
				"is_long_video": 0,
				"single_post_feature_info": {
					"text": title,
					"text_extra": text_extra,
					"markup_text": title,
					"music_info": {},
					"poster_delay": 0,
				}
			}
		]
	}

	if schedule_time > 0:
		data["feature_common_info_list"][0]["schedule_time"] = schedule_time + int(time.time())
	
	print("[TikTok] Data payload prepared, entering upload loop")
	uploaded = False
	while True:
		print("[TikTok] Checking for msToken in session cookies")
		mstoken = session.cookies.get("msToken")
		if not mstoken:
			print("[-] msToken not found in cookies; cannot sign request")
			return False
		print(f"[TikTok] msToken found: {mstoken[:20]}...")
		
		print("[TikTok] Preparing signature generation")
		js_path = os.path.join(os.getcwd(), "app", "vendor", "tiktok_uploader", "tiktok-signature", "browser.js")
		sig_url = f"https://www.tiktok.com/api/v1/web/project/post/?app_name=tiktok_web&channel=tiktok_web&device_platform=web&aid=1988&msToken={mstoken}"
		print(f"[TikTok] Calling signature generator: {js_path}")
		signatures = subprocess_jsvmp(js_path, user_agent, sig_url)
		print(f"[TikTok] Signature generator returned: {len(signatures) if signatures else 0} chars")
		if signatures is None:
			print("[-] Failed to generate signatures")
			return False

		print("[TikTok] Parsing signature response")
		try:
			tt_output = json.loads(signatures)["data"]
			print(f"[TikTok] Signature parsed successfully, keys: {list(tt_output.keys()) if tt_output else 'None'}")
		except (json.JSONDecodeError, KeyError) as e:
			print(f"[-] Failed to parse signature data: {str(e)}")
			return False

		project_post_dict = {
			"app_name": "tiktok_web",
			"channel": "tiktok_web",
			"device_platform": "web",
			"aid": 1988,
			"msToken": mstoken,
			"_signature": tt_output["signature"],
		}
		try:
			xb = (tt_output or {}).get("x-bogus")
			if xb:
				project_post_dict["X-Bogus"] = xb
		except Exception:
			pass

		print(f"[TikTok] POST params: {list(project_post_dict.keys())}")
		print(f"[TikTok] Data payload keys: {list(data.keys())}")
		print(f"[TikTok] msToken: {mstoken[:10]}...")
		print(f"[TikTok] Signature: {tt_output['signature'][:20]}...")
		print(f"[TikTok] Full data payload: {json.dumps(data, indent=2)[:500]}...")

		url = f"https://www.tiktok.com/tiktok/web/project/post/v1/"
		print("[TikTok] Posting project (signatures ready)")
		r = session.request("POST", url, params=project_post_dict, data=json.dumps(data), headers=headers, timeout=_TIMEOUT)
		
		print(f"[TikTok] Response status: {r.status_code}")
		print(f"[TikTok] Response text: {r.text[:200]}...")
		
		if not assertSuccess(url, r):
			print("[-] Published failed, try later again")
			printError(url, r)
			return False

		if r.json()["status_code"] == 0:
			print(f"Published successfully {'| Scheduled for ' + str(schedule_time) if schedule_time else ''}")
			uploaded = True
			break
		else:
			print("[-] Publish failed to Tiktok, trying again...")
			printError(url, r)
			return False
	if not uploaded:
		print("[-] Could not upload video")
		return False


def upload_to_tiktok(video_file, session):
	url = "https://www.tiktok.com/api/v1/video/upload/auth/?aid=1988"
	print("[TikTok] Requesting video upload auth")
	r = session.get(url, timeout=_TIMEOUT)
	if not assert_success(url, r):
		return False

	aws_auth = AWSSigV4(
		"vod",
		region="ap-singapore-1",
		aws_access_key_id=r.json()["video_token_v5"]["access_key_id"],
		aws_secret_access_key=r.json()["video_token_v5"]["secret_acess_key"],
		aws_session_token=r.json()["video_token_v5"]["session_token"],
	)
	with open(os.path.join(os.getcwd(), Config.get().videos_dir, video_file), "rb") as f:
		video_content = f.read()
	file_size = len(video_content)
	url = f"https://www.tiktok.com/top/v1?Action=ApplyUploadInner&Version=2020-11-19&SpaceName=tiktok&FileType=video&IsInner=1&FileSize={file_size}&s=g158iqx8434"

	print("[TikTok] ApplyUploadInner")
	r = session.get(url, auth=aws_auth, timeout=_TIMEOUT)
	if not assert_success(url, r):
		return False

	upload_node = r.json()["Result"]["InnerUploadAddress"]["UploadNodes"][0]
	video_id = upload_node["Vid"]
	store_uri = upload_node["StoreInfos"][0]["StoreUri"]
	video_auth = upload_node["StoreInfos"][0]["Auth"]
	upload_host = upload_node["UploadHost"]
	session_key = upload_node["SessionKey"]
	chunk_size = 5242880
	chunks = []
	i = 0
	while i < file_size:
		chunks.append(video_content[i: i + chunk_size])
		i += chunk_size
	crcs = []
	upload_id = str(uuid.uuid4())
	print(f"[TikTok] Uploading {len(chunks)} chunks of size 5MB")
	for i in range(len(chunks)):
		chunk = chunks[i]
		crc = crc32(chunk)
		crcs.append(crc)
		url = f"https://{upload_host}/{store_uri}?partNumber={i + 1}&uploadID={upload_id}&phase=transfer"
		headers = {
			"Authorization": video_auth,
			"Content-Type": "application/octet-stream",
			"Content-Disposition": 'attachment; filename="undefined"',
			"Content-Crc32": crc,
		}

		try:
			r = session.post(url, headers=headers, data=chunk, timeout=_TIMEOUT)
		except requests.exceptions.RequestException as e:
			print(f"[TikTok] Chunk {i+1}/{len(chunks)} failed: {e}")
			return False
		if (i + 1) % 20 == 0 or i == len(chunks) - 1:
			print(f"[TikTok] Uploaded chunk {i+1}/{len(chunks)}")

	return video_id, session_key, upload_id, crcs, upload_host, store_uri, video_auth, aws_auth
