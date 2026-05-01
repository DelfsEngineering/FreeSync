"""
High-level flow for blue/green FileMaker mirroring.

Assumptions you should confirm for your deployment:

- Two hosted `.fmp12` files (or two FM Cloud apps) represent **blue** and **green**.
- Mirroring is **not** built into FileMaker Server; you implement it via Data API,
  ESS/ODBC, flat-file exchange, or FM scripts triggered externally — this service is
  the orchestration shell.

Replace `run_sync_cycle` with your direction (e.g. blue → green cutover prep,
bidirectional with conflict rules, or read-only replica refresh).
"""

from __future__ import annotations

import asyncio
import logging
import os
import signal

from fremirror.config import Settings, validate_pair

logger = logging.getLogger(__name__)


def configure_logging(level: str) -> None:
    logging.basicConfig(
        level=getattr(logging, level.upper(), logging.INFO),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )


async def run_sync_cycle(settings: Settings) -> None:
    """
    One logical sync pass between blue and green.

    Wire this to FileMaker Data API (HTTPS + token/session), or call FM scripts via API,
    or push/pull records per your conflict strategy.
    """
    logger.info(
        "Sync cycle (stub): blue=%s/%s green=%s/%s",
        settings.fm_blue_base_url,
        settings.fm_blue_database,
        settings.fm_green_base_url,
        settings.fm_green_database,
    )
    # TODO: implement record/layout sync; keep idempotent for safe reruns.


async def run_forever(settings: Settings) -> None:
    validate_pair(settings)
    stop = asyncio.Event()

    def _halt() -> None:
        stop.set()

    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        try:
            loop.add_signal_handler(sig, _halt)
        except NotImplementedError:
            # Windows / restricted contexts
            pass

    while not stop.is_set():
        try:
            await run_sync_cycle(settings)
        except Exception:
            logger.exception("Sync cycle failed")
        if settings.sync_interval_sec <= 0:
            break
        try:
            await asyncio.wait_for(stop.wait(), timeout=float(settings.sync_interval_sec))
        except asyncio.TimeoutError:
            continue


def main() -> None:
    settings = Settings()
    configure_logging(settings.log_level)
    if os.environ.get("FREEMIRROR_ONCE") == "1":
        settings.sync_interval_sec = 0
    asyncio.run(run_forever(settings))
