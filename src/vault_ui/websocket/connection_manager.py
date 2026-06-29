"""WebSocket connection management."""

import json
import logging
from typing import Any

from fastapi import WebSocket

logger = logging.getLogger(__name__)


class ConnectionManager:
    """Manages active WebSocket connections and broadcasts messages."""

    def __init__(self) -> None:
        """Initialize connection manager with empty connection list."""
        self.active_connections: list[WebSocket] = []

    async def connect(self, websocket: WebSocket) -> None:
        """Accept and register a new WebSocket connection.

        Args:
            websocket: WebSocket connection to register
        """
        await websocket.accept()
        self.active_connections.append(websocket)
        logger.info(f"[ConnectionManager] Client connected (total: {len(self.active_connections)})")

    def disconnect(self, websocket: WebSocket) -> None:
        """Remove a WebSocket connection from active list.

        Args:
            websocket: WebSocket connection to remove
        """
        if websocket in self.active_connections:
            self.active_connections.remove(websocket)
            logger.info(
                f"[ConnectionManager] Client disconnected (total: {len(self.active_connections)})"
            )

    async def broadcast(self, message: dict[str, Any]) -> None:
        """Broadcast message to all connected clients.

        Args:
            message: Dictionary to send as JSON to all clients
        """
        if not self.active_connections:
            logger.debug("[ConnectionManager] No active connections to broadcast to")
            return

        message_json = json.dumps(message)
        # Snapshot to avoid mutation during iteration
        connections = list(self.active_connections)
        num_clients = len(connections)
        logger.debug(f"[ConnectionManager] Broadcasting to {num_clients} clients: {message_json}")

        # Send to all connections, remove dead ones
        dead_connections = []
        for connection in connections:
            try:
                await connection.send_text(message_json)
            except Exception as e:
                logger.warning(f"[ConnectionManager] Failed to send to client: {e}", exc_info=True)
                dead_connections.append(connection)

        # Clean up dead connections
        for connection in dead_connections:
            self.disconnect(connection)

    async def send_personal(self, message: dict[str, Any], websocket: WebSocket) -> None:
        """Send message to specific client.

        Args:
            message: Dictionary to send as JSON
            websocket: Target WebSocket connection
        """
        try:
            await websocket.send_text(json.dumps(message))
        except Exception as e:
            logger.warning(
                f"[ConnectionManager] Failed to send personal message: {e}", exc_info=True
            )
            self.disconnect(websocket)
