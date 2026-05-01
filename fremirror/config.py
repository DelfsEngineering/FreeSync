from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    """Load from environment / `.env` for Docker and local runs."""

    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8", extra="ignore")

    # Blue side (current primary)
    fm_blue_base_url: str = ""
    fm_blue_database: str = ""
    fm_blue_user: str = ""
    fm_blue_password: str = ""

    # Green side (standby / next release)
    fm_green_base_url: str = ""
    fm_green_database: str = ""
    fm_green_user: str = ""
    fm_green_password: str = ""

    # Sync loop (seconds); set 0 to run once and exit (good for cron/k8s Job)
    sync_interval_sec: int = 300

    # Placeholder: extend with layout/table lists and direction when you define schema
    log_level: str = "INFO"


def validate_pair(settings: Settings) -> None:
    """Fail fast if half-configured (omit checks until you wire real sync)."""
    need = [
        settings.fm_blue_base_url,
        settings.fm_green_base_url,
        settings.fm_blue_database,
        settings.fm_green_database,
    ]
    if not all(need):
        raise ValueError(
            "Set FM_BLUE_BASE_URL, FM_GREEN_BASE_URL, FM_BLUE_DATABASE, FM_GREEN_DATABASE (and auth)."
        )
