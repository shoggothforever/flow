#!/usr/bin/env python3
"""Deterministic stdio peer for the browser smoke test; never contacts a model."""
import json
import os
import sys
import time
import uuid

if "--version" in sys.argv:
    print("codex-cli browser-fixture")
    sys.exit(0)

initialized = False
state_path = os.environ["FLOW_CODEX_FIXTURE"]
for line in sys.stdin:
    req = json.loads(line)
    method = req.get("method")
    with open(state_path + ".calls", "a", encoding="utf-8") as log:
        log.write(method + "\n")
    if method == "initialized":
        initialized = True
        continue
    result = None
    error = None
    params = req.get("params", {})
    with open(state_path, encoding="utf-8") as file:
        sessions = json.load(file)
    if method == "initialize":
        result = {"userAgent": "flow-browser-fixture"}
    elif not initialized:
        error = {"code": -1, "message": "not initialized"}
    elif method == "thread/list":
        result = {"data": [] if params.get("archived") else sessions, "nextCursor": None}
    elif method == "thread/read":
        found = next((x for x in sessions if x["id"] == params["threadId"]), None)
        if found:
            result = {"thread": found}
        else:
            error = {"code": -32600, "message": "thread not found"}
    elif method == "thread/fork":
        if params.get("ephemeral") is not False or params.get("excludeTurns") is not True:
            raise RuntimeError("Fork must be persistent and metadata-only")
        parent = next(x for x in sessions if x["id"] == params["threadId"])
        child = dict(parent, id=str(uuid.uuid4()), forkedFromId=parent["id"], name="需求调整 · 新分支", createdAt=int(time.time()))
        sessions.append(child)
        with open(state_path, "w", encoding="utf-8") as file:
            json.dump(sessions, file)
        if os.path.exists(state_path + ".lose-fork-response"):
            os.remove(state_path + ".lose-fork-response")
            sys.exit(0)
        result = {"thread": child}
    else:
        raise RuntimeError("Unexpected method (model turns are forbidden): " + str(method))
    print(json.dumps({"id": req["id"], **({"error": error} if error else {"result": result})}), flush=True)
