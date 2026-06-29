"""Configuration for vault-ui."""

import json
import logging
import subprocess
from dataclasses import dataclass, field
from pathlib import Path

import yaml

logger = logging.getLogger(__name__)


@dataclass
class VaultConfig:
    """Configuration for a single Obsidian vault."""

    name: str
    vault_path: str
    tasks_folder: str
    vault_name: str = ""  # For obsidian:// URLs, defaults to name.title()
    claude_script: str = "claude"  # Script to run Claude sessions (default: "claude")
    vault_cli_path: str = "vault-cli"  # Path to vault-cli binary
    session_project_dir: str = ""  # Override Claude project dir for session file lookup


@dataclass
class Config:
    """Application configuration."""

    vaults: list[VaultConfig] = field(default_factory=list)
    host: str = "127.0.0.1"
    port: int = 8000
    current_user: str = ""

    def get_vault(self, name: str) -> VaultConfig | None:
        """Get vault config by name."""
        for vault in self.vaults:
            if vault.name == name:
                return vault
        return None


_CONFIG_PATH = Path(__file__).parent.parent.parent / "config.yaml"


def discover_current_user(vault_cli_path: str) -> str:
    """Call vault-cli config current-user and return the username."""
    result = subprocess.run(
        [vault_cli_path, "config", "current-user"],
        capture_output=True,
        text=True,
        timeout=10,
    )
    if result.returncode != 0:
        raise RuntimeError(f"vault-cli config current-user failed: {result.stderr.strip()}")
    return result.stdout.strip()


def discover_vaults_from_cli(vault_cli_path: str) -> list[dict[str, str]]:
    """Call vault-cli config list --output json and return parsed vault list."""
    result = subprocess.run(
        [vault_cli_path, "config", "list", "--output", "json"],
        capture_output=True,
        text=True,
        timeout=10,
    )
    if result.returncode != 0:
        raise RuntimeError(f"vault-cli config list failed: {result.stderr.strip()}")
    vaults = json.loads(result.stdout)
    if not isinstance(vaults, list):
        raise RuntimeError("vault-cli config list returned non-list")
    return vaults


def load_config(config_path: Path = _CONFIG_PATH) -> Config:
    """Load configuration from config.yaml. Exits with error if not found."""
    if not config_path.exists():
        raise FileNotFoundError(
            f"config.yaml not found at {config_path}\n"
            "\nCreate it by copying the example:\n"
            "  cp config.yaml.example config.yaml\n"
            "\nThen edit vault paths to match your system."
        )

    with config_path.open() as f:
        data = yaml.safe_load(f)

    vault_cli_path = data.get("vault_cli_path", "vault-cli")
    current_user = discover_current_user(vault_cli_path)
    cli_vaults = discover_vaults_from_cli(vault_cli_path)

    # Index cli vaults by lowercase name for case-insensitive lookup
    cli_vault_by_name = {v["name"].lower(): v for v in cli_vaults}

    vault_overrides = data.get("vaults", {}) or {}

    vaults = []
    for vault_key, overrides in vault_overrides.items():
        overrides = overrides or {}
        cli_vault = cli_vault_by_name.get(vault_key.lower())
        if cli_vault is None:
            logger.warning("Vault '%s' not found in vault-cli output, skipping", vault_key)
            continue

        vault_name = overrides.get("vault_name") or ""
        if not vault_name:
            vault_name = vault_key.title()

        vaults.append(
            VaultConfig(
                name=vault_key,
                vault_path=cli_vault["path"],
                tasks_folder=cli_vault["tasks_dir"],
                vault_name=vault_name,
                claude_script=cli_vault.get("claude_script") or "claude",
                vault_cli_path=vault_cli_path,
                session_project_dir=cli_vault.get("session_project_dir") or "",
            )
        )

    if not vaults:
        raise RuntimeError(
            "No vaults configured after merging with vault-cli output. "
            "Check config.yaml vaults section and that vault-cli is available."
        )

    return Config(
        vaults=vaults,
        host=data.get("host", "127.0.0.1"),
        port=data.get("port", 8000),
        current_user=current_user,
    )
