# Playwright-ready pinned toolchain image (ADR-0001 extension): node:24 with
# chromium and its system libraries baked in. Rebuild when @playwright/test
# version changes (tag includes the version; the Makefile does this).
FROM node:24
ARG PW_VERSION
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
RUN npm i -g playwright@${PW_VERSION} && \
    npx playwright install --with-deps chromium
