#!/usr/bin/env python3
"""Deterministic transport regressions using synthetic replies and a virtual clock."""
import contextlib
import datetime as dt
import email.utils
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

source = Path(__file__).resolve().parents[1] / "internal/api/management-skill/qcontrolhub-manager/scripts/qch.py"
spec = importlib.util.spec_from_file_location("qch", source)
qch = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qch)
TASK = "tsk_1111111111111111"
AGENT = "/agents/agt_1111111111111111"
MIGRATE = AGENT + "/configs/xray/server-inbounds/route/tls-frontend/migrate"


class Transport(unittest.TestCase):
    def run_case(self, args, data, responses):
        calls = []
        elapsed = [0]
        def fetch(opener, request, deadline):
            calls.append((request.method, request.full_url.split("/api/v1")[1]))
            if elapsed[0] >= deadline:
                raise qch.Failure("read_timeout")
            result = responses.pop(0)
            if isinstance(result, Exception):
                raise result
            return result
        def sleep(duration):
            elapsed[0] += duration
        env = {"QCH_API_KEY": "qch_key_test_only", "QCH_API_BASE": "http://127.0.0.1/api/v1"}
        stderr = io.StringIO()
        with patch.dict(qch.os.environ, env, clear=True), patch.object(qch, "fetch", fetch), patch.object(qch.time, "sleep", sleep), patch.object(qch.time, "monotonic", lambda:elapsed[0]), patch.object(qch.sys, "argv", ["qch", *args]), patch.object(qch.sys, "stdin", io.TextIOWrapper(io.BytesIO(data))), contextlib.redirect_stderr(stderr):
            try:
                result = qch.main()
            except qch.Failure as error:
                result = error.result
        return result, calls, stderr.getvalue()

    def test_read_one_private_field(self):
        payload = {"/group": [{"~name": "private canary", "value": None}]}
        path = ["GET", "/configs", "--field", "/~1group/0/~0name"]
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory, "selected.txt")
            for args, expected in (
                (path, {"classification": "private_destination_required"}),
                (path + ["--download", str(target)], {"bytes_saved": len("private canary")}),
                (path + ["--download", str(target)], {"classification": "download_destination_unavailable"}),
                (["GET", "/configs", "--field", "/~1group/0/value"], {"value": None}),
                (["GET", "/configs", "--field", "/~1group/1"], {"classification": "field_missing"}),
                (["GET", "/configs", "--field", "/~1group"], {"classification": "scalar_field_required"}),
            ):
                result, calls, stderr = self.run_case(args, b"", [(200, {}, payload)])
                self.assertEqual(result, expected)
                self.assertEqual(len(calls), 1)
                self.assertEqual(stderr, "")
            self.assertEqual(target.read_text(), "private canary")
            if qch.os.name == "posix":
                self.assertEqual(target.stat().st_mode & 0o077, 0)

    def test_fields_on_log_and_tcp_parameter_rows(self):
        for route, field in (("/core-logs", "id"), ("/system-tcp/parameters", "min")):
            result, calls, _ = self.run_case(
                ["GET", route, "--field", "/0/" + field], b"", [(200, {}, [{field: 1}])]
            )
            self.assertEqual(result, {"value": 1})
            self.assertEqual(calls, [("GET", route)])

    def test_receipts_drop_free_text(self):
        receipt = {"id": "trf_1111111111111111", "status": "not_executed", "message": "private canary"}
        safe = {"id": receipt["id"], "status": receipt["status"]}
        for payload, expected in (([receipt], [safe]), ({"results": [receipt]}, {"results": [safe]})):
            self.assertEqual(qch.project("/traffic-policies", payload, {}), expected)

    def test_invalid_download_and_field_targets_make_no_request(self):
        for args in (
            ["GET", "/users", "--download", "unused"],
            ["GET", "/auth/session", "--field", "/name"],
            ["POST", "/configs", "--field", "/id"],
            ["GET", "/configs", "--field", "/bad~path"],
        ):
            result, calls, _ = self.run_case(args, b"", [])
            self.assertIn(result["classification"], {"invalid_download_target", "invalid_field_pointer"})
            self.assertEqual(calls, [])

    def test_path_evidence_and_unknown_blocker(self):
        row = {"config_id": "cfg_1111111111111111", "instance_id": "", "engine": "xray",
               "tag": "private-inbound-name", "path": "/private-route", "candidate": "failed", "recovery": "succeeded"}
        projected = qch.migration({"result": {"paths": [row]}})["result"]["paths"][0]
        self.assertEqual(projected["config_id"], row["config_id"])
        self.assertEqual(projected["instance_id"], "")
        self.assertEqual(projected["recovery"], "succeeded")
        self.assertEqual(projected["path_sha256"], qch.hashlib.sha256(row["path"].encode()).hexdigest())
        self.assertNotIn("private", json.dumps(projected))
        self.assertEqual(qch.preview({"blockers": ["sensitive-unknown-code"]})["blockers"], ["unknown_blocker"])

    def test_timeout_queries_migration(self):
        result,calls,_ = self.run_case(["POST",MIGRATE,"--stdin"],json.dumps({"task_id":TASK}).encode(),[qch.Failure("read_timeout"),(200,{},{"task_id":TASK,"state":"running"})])
        self.assertEqual(calls,[("POST",MIGRATE),("GET",AGENT+"/tls-migrations/"+TASK)])
        self.assertEqual(result["state"],"running")

    def test_conflicting_preferences(self):
        payload = {"values":{"shell":{"collapsed":True}},"revision":2}
        result,calls,_=self.run_case(["PATCH","/preferences/ui","--stdin"],json.dumps(payload).encode(),[(409,{},{}),(200,{},{"revision":3,"values":{"shell":{"collapsed":False}}})])
        self.assertEqual(calls,[("PATCH","/preferences/ui"),("GET","/preferences/ui")])
        self.assertIs(result["effect_present"],False)
        self.assertEqual(result["revision"],3)

    def test_retry_after(self):
        date=email.utils.format_datetime(dt.datetime.now(dt.timezone.utc)+dt.timedelta(minutes=3),usegmt=True)
        for delay in ("120",date):
            result,calls,_=self.run_case(["GET","/tasks","--budget","1"],b"",[(429,{"Retry-After":delay},{})])
            self.assertGreaterEqual(result["retry_after_seconds"],120)
            self.assertEqual(len(calls),1)

    def test_poll_deadline_and_filters(self):
        sample = "qch_key_synthetic_secondkeyvalue"
        body = {"id":TASK,"status":"running","output":{"message":"Bearer "+sample},"error":"password: "+sample,"logs":[{"id":sample}],"content":json.dumps({"token":sample})}
        result,_,err=self.run_case(["GET","/tasks/"+TASK,"--wait","--budget","1"],b"",[(200,{},body)])
        self.assertEqual(result,{"classification":"pending","id":TASK,"status":"running"})
        self.assertNotIn("secondkeyvalue",err)
        for response in (body, None):
            result,_,err=self.run_case(["GET","/tasks/"+TASK],b"",[(500,{"X-Request-ID":sample},response)])
            self.assertEqual(result,{"classification":"server_failure","status":500})
            self.assertEqual(err,"")


if __name__ == "__main__":
    unittest.main()
