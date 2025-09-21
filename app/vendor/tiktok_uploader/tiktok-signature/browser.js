const Signer = require("./index");

var url = process.argv[2];
var userAgent = process.argv[3];

(async function main() {
  let signer = null;

  try {
    console.error("Starting browser initialization...");
    const signer = new Signer(undefined, userAgent);
    
    console.error("Calling signer.init()...");
    await signer.init();
    
    console.error("Generating signature...");
    const sign = await signer.sign(url);
    
    console.error("Getting navigator info...");
    const navigator = await signer.navigator();

    let output = JSON.stringify({
      status: "ok",
      data: {
        ...sign,
        navigator: navigator,
      },
    });
    console.log(output);
    await signer.close();
  } catch (err) {
    console.error(JSON.stringify({
      status: "error",
      error: err.message || err.toString()
    }));
    if (signer) {
      try {
        await signer.close();
      } catch (closeErr) {
        // ignore close errors
      }
    }
    process.exit(1);
  }
})();
