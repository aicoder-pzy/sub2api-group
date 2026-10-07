"""Offline container check: launch the actual sandboxed browser without OAuth."""
import os
from playwright.sync_api import sync_playwright

assert os.geteuid() != 0, "Browser must run without root"
with sync_playwright() as playwright:
    browser = playwright.chromium.launch(
        executable_path=os.environ["PRISM_ADAPTER_CHROME"],
        headless=True,
        chromium_sandbox=True,
    )
    page = browser.new_page()
    page.goto("data:text/html,<title>Prism sandbox smoke</title><p>OK</p>")
    assert page.title() == "Prism sandbox smoke"
    assert page.locator("p").inner_text() == "OK"
    browser.close()
print("Non-root Chromium sandbox smoke passed (no upstream request)")
