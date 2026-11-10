#!/usr/bin/env python3
"""QControlHub API transport. Credentials stay in the client runtime."""
import argparse
import ipaddress
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, file, code, message, headers, target):
        return None


def target_url(base, path):
    base_url = urllib.parse.urlsplit(base)
    try:
        loopback = ipaddress.ip_address(base_url.hostname or '').is_loopback
    except ValueError:
        loopback = base_url.hostname == 'localhost'
    if (base_url.scheme != 'https' and not (base_url.scheme == 'http' and loopback)) or not base_url.hostname:
        raise ValueError('QCH_API_BASE requires HTTPS (HTTP is allowed only for loopback tests)')
    if base_url.username or base_url.password or base_url.query or base_url.fragment or not base_url.path.rstrip('/').endswith('/api/v1'):
        raise ValueError('QCH_API_BASE must be an API root without credentials, query or fragment')
    request_url = urllib.parse.urlsplit(path)
    decoded = urllib.parse.unquote(request_url.path)
    if not path.startswith('/') or request_url.scheme or request_url.netloc or request_url.fragment or '\\' in decoded or any(segment in ('.', '..') for segment in decoded.split('/')) or decoded.startswith('//'):
        raise ValueError('request path must be relative to the configured API root')
    return base.rstrip('/') + path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('method', choices=['GET', 'POST', 'PUT', 'DELETE'])
    parser.add_argument('path')
    parser.add_argument('--body-file')
    args = parser.parse_args()
    try:
        url = target_url(os.environ.get('QCH_API_BASE', ''), args.path)
        key = os.environ.get('QCH_API_KEY', '')
        if not key.startswith('qch_key_') or any(c.isspace() for c in key):
            raise ValueError('QCH_API_KEY must contain the configured API Key')
        body = None
        if args.body_file:
            with open(args.body_file, 'rb') as source:
                body = source.read(16 * 1024 * 1024 + 1)
            if len(body) > 16 * 1024 * 1024:
                raise ValueError('request body exceeds 16 MiB')
            json.loads(body)
        if args.method == 'GET' and body is not None:
            raise ValueError('GET does not accept a request body')
        opener = urllib.request.build_opener(NoRedirect())
        attempts = 3 if args.method == 'GET' else 1
        for attempt in range(attempts):
            request = urllib.request.Request(url, data=body, method=args.method, headers={
                'Authorization': 'Bearer ' + key, 'Accept': 'application/json',
                'Content-Type': 'application/json', 'User-Agent': 'QControlHub-Manager/1.0'})
            try:
                with opener.open(request, timeout=30) as response:
                    payload = response.read(32 * 1024 * 1024 + 1)
                    if len(payload) > 32 * 1024 * 1024:
                        raise ValueError('response exceeds 32 MiB; narrow the query')
                    sys.stdout.buffer.write(payload.replace(key.encode(), b'[redacted]'))
                return 0
            except urllib.error.HTTPError as error:
                if error.code == 429 and attempt + 1 < attempts:
                    try:
                        wait = min(30, max(1, int(error.headers.get('Retry-After', '2'))))
                    except ValueError:
                        wait = 2
                    time.sleep(wait)
                    continue
                detail = error.read(65536).decode('utf-8', errors='replace').replace(key, '[redacted]')
                print(json.dumps({'status': error.code, 'request_id': error.headers.get('X-Request-ID'), 'error': detail}, ensure_ascii=False), file=sys.stderr)
                return 1
        return 1
    except (ValueError, OSError, urllib.error.URLError) as error:
        # Do not print exception URLs: a misconfigured base could contain credentials.
        print(json.dumps({'error': 'Transport/input failure', 'result': 'A submitted write may have completed; query the existing resource before retrying.' if args.method != 'GET' else 'Read did not complete.', 'type': type(error).__name__}), file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.exit(main())
