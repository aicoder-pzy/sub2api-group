FROM python:3.12-slim-bookworm

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
WORKDIR /opt/prism
COPY requirements.txt ./
RUN pip install --no-cache-dir --only-binary=:all: -r requirements.txt \
    && python -m playwright install --with-deps --only-shell chromium \
    && rm -rf /var/lib/apt/lists/* \
    && chrome=$(find /ms-playwright -type f -name chrome-headless-shell | head -n 1) \
    && sandbox=$(find /ms-playwright -type f \( -name chrome_sandbox -o -name chrome-sandbox \) | head -n 1) \
    && test -n "$chrome" && test -n "$sandbox" \
    && chown root:root "$sandbox" && chmod 4755 "$sandbox" \
    && ln -s "$chrome" /opt/prism/chrome \
    && ln -s "$sandbox" /opt/prism/chrome-sandbox \
    && useradd --uid 10001 --create-home prism \
    && install -d -o prism -g prism -m 0700 /var/lib/sub2api-prism
COPY *.py ./
ENV PRISM_ADAPTER_CHROME=/opt/prism/chrome \
    CHROME_DEVEL_SANDBOX=/opt/prism/chrome-sandbox \
    PRISM_ADAPTER_STATE_DIR=/var/lib/sub2api-prism \
    PRISM_ADAPTER_MODE=browser \
    PRISM_ADAPTER_CLIENT_TOOLS_ENABLED=false
USER 10001:10001
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8319/health', timeout=3)"
CMD ["python", "server.py"]
