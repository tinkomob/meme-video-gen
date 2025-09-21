const { createCipheriv } = require("crypto");
const { devices, chromium } = require("playwright-chromium");
const Utils = require("./utils");
const iPhone11 = devices["iPhone 11 Pro"];
class Signer {
  userAgent =
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/98.0.4758.109 Safari/537.36";
  args = [
    "--disable-blink-features",
    "--disable-blink-features=AutomationControlled",
    "--disable-infobars",
    "--window-size=1920,1080",
    "--start-maximized",
    "--no-sandbox",
    "--disable-setuid-sandbox",
    "--disable-dev-shm-usage",
    "--disable-gpu",
    "--disable-web-security",
  ];
  default_url = "https://www.tiktok.com/?lang=en";
  password = "webapp1.0+202106";
  constructor(default_url, userAgent, browser) {
    if (default_url) {
      this.default_url = default_url;
    }
    this.userAgent = userAgent || this.userAgent;
    if (browser) {
      this.browser = browser;
      this.isExternalBrowser = true;
    }
    this.args.push(`--user-agent="${this.userAgent}"`);
    this.options = {
      headless: true,
      args: this.args,
      ignoreDefaultArgs: ["--mute-audio", "--hide-scrollbars"],
      ignoreHTTPSErrors: true,
      timeout: 45000,
    };
  }
  async init() {
    if (!this.browser) {
      this.browser = await chromium.launch(this.options);
    }
    let emulateTemplate = {
      ...iPhone11,
      locale: "en-US",
      deviceScaleFactor: Utils.getRandomInt(1, 3),
      isMobile: Math.random() > 0.5,
      hasTouch: Math.random() > 0.5,
      userAgent: this.userAgent,
    };
    emulateTemplate.viewport.width = Utils.getRandomInt(320, 1920);
    emulateTemplate.viewport.height = Utils.getRandomInt(320, 1920);
    this.context = await this.browser.newContext({
      bypassCSP: true,
      ...emulateTemplate,
    });
    await this.context.addInitScript(() => {
      Object.defineProperty(navigator, 'webdriver', { get: () => false });
    });
    this.page = await this.context.newPage();
    this.page.setDefaultTimeout(45000);
    this.page.setDefaultNavigationTimeout(45000);
    await this.page.goto(this.default_url, {
      waitUntil: "domcontentloaded",
      timeout: 30000,
    });
    await this.page.waitForFunction(() => {
      return !!(window && window.byted_acrawler && typeof window.byted_acrawler.sign === 'function');
    }, { timeout: 20000 });
    await this.page.evaluate(() => {
      window.generateSignature = function generateSignature(url) {
        if (window && window.byted_acrawler && typeof window.byted_acrawler.sign === "function") {
          return window.byted_acrawler.sign({ url });
        }
        throw "No signature function found on page";
      };
      window.generateBogusWrapper = function generateBogusWrapper(params) {
        try {
          if (typeof window.generateBogus === "function") {
            return window.generateBogus(params);
          }
        } catch (e) {}
        return "";
      };
      return this;
    });
  }
  async navigator() {
    const info = await this.page.evaluate(() => {
      return {
        deviceScaleFactor: window.devicePixelRatio,
        user_agent: window.navigator.userAgent,
        browser_language: window.navigator.language,
        browser_platform: window.navigator.platform,
        browser_name: window.navigator.appCodeName,
        browser_version: window.navigator.appVersion,
      };
    });
    return info;
  }
  async sign(link) {
    let verify_fp = Utils.generateVerifyFp();
    let newUrl = link + "&verifyFp=" + verify_fp;
    let token = await this.page.evaluate(`generateSignature("${newUrl}")`);
    let signed_url = newUrl + "&_signature=" + token;
    let queryString = new URL(signed_url).searchParams.toString();
    let bogus = await this.page.evaluate(`generateBogusWrapper("${queryString}")`);
    if (bogus) {
      signed_url += "&X-Bogus=" + bogus;
    }

    return {
      signature: token,
      verify_fp: verify_fp,
      signed_url: signed_url,
      "x-tt-params": this.xttparams(queryString),
      "x-bogus": bogus,
    };
  }
  xttparams(query_str) {
    query_str += "&is_encryption=1";
    const cipher = createCipheriv("aes-128-cbc", this.password, this.password);
    return Buffer.concat([cipher.update(query_str), cipher.final()]).toString(
      "base64"
    );
  }
  async close() {
    if (this.browser && !this.isExternalBrowser) {
      await this.browser.close();
      this.browser = null;
    }
    if (this.page) {
      this.page = null;
    }
  }
}
module.exports = Signer;
