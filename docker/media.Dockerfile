# Canopy media-processor runtime (T14; OCR tools added for T30 receipt
# extraction): libvips + HEIF decode plugin + tesseract + poppler.
# No encoders beyond jpegsave's, no shell utilities beyond the base.
# Base pinned by digest (ADR-0001 / T00 §4). Package versions observed at
# build time (Ubuntu 24.04 line): libvips-tools 8.15.1-1.1build4,
# libheif1 1.17.6-1ubuntu4.9, libheif-plugin-libde265 1.17.6-1ubuntu4.9,
# tesseract-ocr 5.3.4-1build5, poppler-utils 24.02.0-1ubuntu9.11
# (licenses per ADR-0001: tesseract Apache-2.0, poppler GPL-2.0+/MIT dual).
# FINDING (T00 step4): Ubuntu 24.04 libheif is plugin-split; the decode
# plugin package is mandatory for phone HEIC.
FROM ubuntu:24.04@sha256:fe81560990fae86dc4743202b2cb4d3df98d0bcf949173cecef15ce1127ef852

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      libvips-tools \
      libheif1 \
      libheif-plugin-libde265 \
      tesseract-ocr \
      poppler-utils \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /work
