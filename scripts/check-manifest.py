import json, sys

# Checks the manifest the built binary prints (protojson field names).
m = json.load(open(sys.argv[1]))
assert m['pluginId'] == 'bloem.pastime'
assert len(m['checksum']) == 64
assert 'metadata' not in m

# One ordinary watch-sync capability. Its ID keeps the provider key
# plugin:<installation>:pastime that existing connections are stored under.
assert [c['type'] for c in m['capabilities']] == ['watch_sync_provider.v1']
cap = m['capabilities'][0]
assert cap['id'] == 'pastime'
assert 'metadata' not in cap, 'no managed-tracking metadata'
assert not cap.get('configSchema'), 'no per-connection settings'
d = cap['watchSyncProvider']
assert d['authMethods'] == ['WATCH_SYNC_AUTH_METHOD_API_KEY']
assert d['scrobblePlayback'] is True
assert d['exportWatched'] is True
assert d['maxBatchSize'] == 100
assert not any(d.get(k, False) for k in ['exportUnwatched', 'syncDropped', 'providesWatchlistOrder'])
assert all(d.get(k, False) for k in ['exportRatings', 'importRatings', 'exportFavorites', 'importFavorites', 'removeFavorites', 'exportWatchlist', 'importWatchlist', 'removeWatchlist', 'importWatched', 'importProgress'])

# The Pastime address is installation-level and public; nothing is secret.
schemas = m['globalConfigSchema']
assert [s['key'] for s in schemas] == ['pastime']
s = schemas[0]
assert s['required'] is True
assert json.loads(s['jsonSchema'])['required'] == ['pastime_url']
assert list(json.loads(s['jsonSchema'])['properties']) == ['pastime_url']
fields = s['adminForm']['fields']
assert [f['key'] for f in fields] == ['pastime_url']
assert not any(f.get('secret') or f.get('control') == 'ADMIN_FORM_CONTROL_PASSWORD' for f in fields)
assert 'writeOnly' not in s['jsonSchema'] and 'secret' not in s['jsonSchema']

assert {(p['os'], p['arch']) for p in m['supportedPlatforms']} == {('linux', 'amd64'), ('linux', 'arm64'), ('darwin', 'arm64')}
text = json.dumps(m).lower()
assert 'managed' not in text and 'bloem managed' not in text
print('Manifest valid: one API-key watch-sync capability and a public Pastime address setting.')
