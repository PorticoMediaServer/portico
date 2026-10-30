#!/usr/bin/env python3
"""Read actual EPUB, PDF, CBZ and CBR bodies through publication sessions."""
import argparse
import io
import json
import urllib.parse
import urllib.request
from pathlib import Path
from PIL import Image
from seed_state import Client, private_json, sign_in, select_profile, items


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', default='http://localhost:49300')
    parser.add_argument('--credentials', type=Path, default=Path('/home/justin/projects/Portico/.private/walks/credentials.json'))
    parser.add_argument('--report', type=Path, required=True)
    parser.add_argument('--library-id', action='append', default=[], help='Known reading library; repeat to avoid a global catalogue fence during ingestion')
    args = parser.parse_args()
    cfg = private_json(args.credentials)
    client = Client(args.base)
    device = None
    report = {'checks': [], 'errors': []}
    try:
        signed, client.token = sign_in(client, cfg['owner']['username'], cfg['owner']['password'])
        device = signed.get('session', signed)['deviceId']
        client.token = select_profile(client, next(p['id'] for p in signed['profiles'] if p.get('primary')))
        libraries = ([{'id': value} for value in args.library_id] if args.library_id
                     else items(client.read('/v1/libraries')))
        selected = {}
        for library in libraries:
            if not args.library_id and library.get('name') not in ('Synthetic Books', 'Synthetic Comics'):
                continue
            document = client.read('/v1/reading/items?' + urllib.parse.urlencode({'libraryId': library['id'], 'limit': 100}))
            for entry in document.get('items', []):
                selected.setdefault(entry.get('format'), entry)
        for fmt in ('epub', 'pdf', 'cbz', 'cbr'):
            session = None
            try:
                entry = selected[fmt]
                session = client.call('POST', '/v1/reading/sessions', {'itemId': entry['id'], 'sourceId': entry['sourceId']}, ok=(200, 201))
                path = '/v1/reading/sessions/' + urllib.parse.quote(session['sessionId'], safe='')
                if fmt == 'epub':
                    path += '/assets?' + urllib.parse.urlencode({'path': session['spine'][0]})
                elif fmt == 'pdf':
                    path += '/source'
                else:
                    path += '/pages/0?width=640'
                headers = {
                    'Authorization': 'Bearer ' + client.token,
                    'X-Portico-Installation-Id': client.installation,
                    'If-Match': '"' + session['revision'] + '"',
                }
                if fmt == 'pdf':
                    headers['Range'] = 'bytes=0-' + str(min(session['size'], 65536) - 1)
                request = urllib.request.Request(client.base + path, headers=headers)
                with client.opener.open(request, timeout=60) as response:
                    data = response.read(8 * 1024 * 1024)
                    content_type = response.headers.get('Content-Type', '')
                if fmt == 'pdf':
                    assert data.startswith(b'%PDF-'), 'PDF signature missing'
                elif fmt == 'epub':
                    assert b'<html' in data and b'<body' in data, 'EPUB chapter missing'
                else:
                    with Image.open(io.BytesIO(data)) as image:
                        image.verify()
                report['checks'].append({'format': fmt, 'bytes': len(data), 'contentType': content_type, 'passed': True})
            except Exception as error:
                report['errors'].append({'format': fmt, 'error': getattr(error, 'code', type(error).__name__), 'status': getattr(error, 'status', None)})
            finally:
                if session:
                    client.call('DELETE', '/v1/reading/sessions/' + urllib.parse.quote(session['sessionId'], safe=''), ok=(204,))
    except Exception as error:
        report['errors'].append({'step': 'setup', 'error': getattr(error, 'code', type(error).__name__), 'status': getattr(error, 'status', None)})
    finally:
        if device:
            client.call('DELETE', '/v1/devices/' + device, ok=(204,))
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps({'checks': len(report['checks']), 'errors': len(report['errors'])}))
    return int(bool(report['errors']))


if __name__ == '__main__':
    raise SystemExit(main())
