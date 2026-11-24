#!/usr/bin/env python3
"""Look up operations in the OpenAPI contract shipped with this Skill."""
import argparse
import json

from qch import catalog


def access(operation):
    if not operation["x-api-key"]:
        return operation["x-authentication"]
    policy = operation["x-key-policy"]
    mode = "read_write" if policy["write"] else "read_only/read_write"
    joiner = " OR " if policy["any"] else " AND "
    return " ".join(filter(None, (mode, joiner.join(policy["permissions"]))))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    listing = commands.add_parser("list", help="list routes matching all search terms")
    listing.add_argument("terms", nargs="*", help="method, path, summary, operation ID or permission")
    listing.add_argument("--key", action="store_true", help="only routes that support an API Key")
    show = commands.add_parser("show", help="inspect one published method and path template")
    show.add_argument("method", type=str.lower)
    show.add_argument("path", help="full path from the list, including /api/v1 where applicable")
    component = commands.add_parser("component", help="read a referenced contract definition")
    component.add_argument("ref", help="a local $ref such as #/components/schemas/core_Action")
    args = parser.parse_args()
    contract = catalog()
    if args.command == "list":
        for path, methods in contract["paths"].items():
            for method, operation in methods.items():
                if args.key and not operation["x-api-key"]:
                    continue
                row = (method.upper(), path, operation["summary"], access(operation), operation["operationId"])
                search = " ".join(row).casefold()
                if all(term.casefold() in search for term in args.terms):
                    print("\t".join(row))
        return
    if args.command == "show":
        item = contract["paths"].get(args.path, {}).get(args.method)
    else:
        parts = args.ref.split("/")
        if len(parts) != 4 or parts[:2] != ["#", "components"]:
            parser.error("expected #/components/<section>/<name>")
        item = contract["components"].get(parts[2], {}).get(parts[3])
    if item is None:
        parser.error("no matching entry in this release")
    print(json.dumps(item, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
