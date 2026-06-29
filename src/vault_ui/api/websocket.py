"""WebSocket API endpoints for real-time updates."""

import logging
from typing import TYPE_CHECKING

from fastapi import APIRouter, WebSocket, WebSocketDisconnect

if TYPE_CHECKING:
    from vault_ui.websocket.connection_manager import ConnectionManager

logger = logging.getLogger(__name__)

router = APIRouter()

# Global connection manager (injected via set_connection_manager)
_connection_manager: "ConnectionManager | None" = None


def set_connection_manager(manager: "ConnectionManager") -> None:
    """Set global connection manager.

    Args:
        manager: ConnectionManager instance
    """
    global _connection_manager
    _connection_manager = manager


@router.websocket("/ws")
async def websocket_endpoint(websocket: WebSocket) -> None:
    """WebSocket endpoint for real-time task updates.

    Args:
        websocket: WebSocket connection
    """
    if not _connection_manager:
        logger.error("[WebSocket] Connection manager not initialized")
        await websocket.close(code=1011, reason="Server not ready")
        return

    await _connection_manager.connect(websocket)
    try:
        while True:
            # Keep connection alive, handle client messages (ping/pong)
            data = await websocket.receive_text()
            logger.debug(f"[WebSocket] Received from client: {data}")

            # Echo back for now (can be used for ping/pong)
            if data == "ping":
                await websocket.send_text("pong")

    except WebSocketDisconnect:
        logger.info("[WebSocket] Client disconnected normally")
        _connection_manager.disconnect(websocket)
    except Exception as e:
        logger.error(f"[WebSocket] Error: {e}", exc_info=True)
        _connection_manager.disconnect(websocket)
