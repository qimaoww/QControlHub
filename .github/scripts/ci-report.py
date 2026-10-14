#!/usr/bin/env python3
"""Collect comparable GitHub job/step timings without rounding away queue time.

gh authentication is required. Re-runs must specify an attempt: never combine
the successful re-run of one failed job with jobs from an earlier attempt.
"""
import argparse
import datetime
import json
import pathlib
import subprocess


def api(path):
    return json.loads(subprocess.check_output(["gh", "api", path], text=True))


def timestamp(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


def seconds(start, finish):
    return max(0, (timestamp(finish) - timestamp(start)).total_seconds()) if start and finish else None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("run", type=int)
    parser.add_argument("--attempt", type=int, default=1)
    parser.add_argument("--repo", default="qimaoww/QControlHub")
    parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()
    base = f"repos/{args.repo}/actions/runs/{args.run}/attempts/{args.attempt}"
    run = api(base)
    jobs = api(base + "/jobs?per_page=100")["jobs"]
    origin = run["run_started_at"]
    active = [job for job in jobs if job["conclusion"] != "skipped" and not job["name"].startswith("Publish ")]
    result = {
        "run": args.run, "attempt": args.attempt, "url": run["html_url"],
        "headSHA": run["head_sha"], "conclusion": run["conclusion"], "startedAt": origin,
        "checksSeconds": max((seconds(origin, job["completed_at"]) or 0 for job in active), default=0),
        "jobMinutes": sum(seconds(job["started_at"], job["completed_at"]) or 0 for job in active) / 60,
        "billingNote": "Actual aggregate job duration, excluding publication; not GitHub billed minute rounding.",
        "jobs": [{
            "name": job["name"], "attempt": job["run_attempt"], "conclusion": job["conclusion"],
            "queueSeconds": seconds(job["created_at"], job["started_at"]),
            "runSeconds": seconds(job["started_at"], job["completed_at"]),
            "steps": [{"name": step["name"], "conclusion": step["conclusion"],
                       "seconds": seconds(step["started_at"], step["completed_at"])} for step in job["steps"]],
        } for job in active],
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n")
    print(f"{args.run}/{args.attempt}: checks {result['checksSeconds']:.0f}s, aggregate {result['jobMinutes']:.2f}m")


if __name__ == "__main__":
    main()
