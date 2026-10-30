// Klever Node Hub - Config upgrade review
// Before a node's configs are replaced by the new Klever defaults, compares the
// current files with the download (POST /api/nodes/{id}/config/preview) and lets
// the operator keep local values. Exposes window.ConfigUpgrade.review().
//
// review(nodes, network) -> Promise<{cancelled, keep, unsupported}>
//   nodes:       [{id, name}]
//   keep:        {nodeId: [diff ids]} to pass as "keep" to config/upgrade
//   unsupported: ids of nodes whose agent can't compare yet (plain upgrade)

(function() {
    // Files whose values define consensus behaviour; keeping old values here can
    // put the node out of step with the network.
    var CRITICAL = {
        'enableEpochs.yaml': true,
        'genesis.json': true,
        'nodesSetup.json': true,
        'gasScheduleV1.yaml': true
    };

    function esc(s) {
        var d = document.createElement('div');
        d.textContent = s == null ? '' : String(s);
        return d.innerHTML;
    }

    // Decisions are remembered per (id, mine, new) so a batch upgrade only asks
    // once for the same difference, but never applies a choice to a different value.
    function memoKey(it) {
        return it.id + '\u0000' + it.mine + '\u0000' + it.new;
    }

    async function fetchPreview(nodeId, network) {
        var res = await API.postJSON('/api/nodes/' + nodeId + '/config/preview', { network: network });
        if (!res) return { error: 'request failed' };
        if (res.data && res.data.unsupported) return { unsupported: true };
        if (!res.ok || !res.data || !res.data.success) {
            return { error: (res.data && res.data.error) || 'preview failed' };
        }
        try {
            return { preview: JSON.parse(res.data.output) };
        } catch (e) {
            return { error: 'invalid preview response' };
        }
    }

    function renderItem(it, idx, remembered) {
        var keepMine = remembered[memoKey(it)] === true;
        var name = 'cu-' + idx;
        var isLocal = it.kind === 'local_only';
        var isFile = it.kind === 'file';
        var mineLabel = isLocal ? 'Keep' : 'Mine';
        var newLabel = isLocal ? 'Drop' : 'New';

        var detail;
        if (isLocal) {
            detail = '<pre style="margin:4px 0 0;padding:6px 8px;background:var(--bg-primary);border:1px solid var(--border);border-radius:var(--radius-sm);font-size:12px;white-space:pre-wrap;">' + esc(it.mine) + '</pre>';
        } else {
            detail = '<div style="margin-top:4px;font-family:monospace;font-size:12px;word-break:break-all;">' +
                '<span style="color:var(--warning, #e6a817);">' + esc(it.mine) + '</span>' +
                ' <span class="text-muted">&rarr;</span> ' +
                '<span>' + esc(it.new) + '</span></div>';
        }

        return '<div class="cu-item" data-id="' + esc(it.id) + '" style="display:flex;justify-content:space-between;gap:16px;align-items:flex-start;padding:8px 0;border-bottom:1px solid var(--border);">' +
            '<div style="min-width:0;flex:1;">' +
                '<div style="font-family:monospace;font-size:12px;word-break:break-all;">' + esc(isFile ? it.file : it.key) + '</div>' +
                detail +
            '</div>' +
            '<div style="display:flex;gap:12px;flex-shrink:0;font-size:13px;">' +
                '<label><input type="radio" name="' + name + '" value="mine"' + (keepMine ? ' checked' : '') + '> ' + mineLabel + '</label>' +
                '<label><input type="radio" name="' + name + '" value="new"' + (keepMine ? '' : ' checked') + '> ' + newLabel + '</label>' +
            '</div>' +
        '</div>';
    }

    // Shows the differences of one node. Resolves to the ids to keep, or null
    // when the operator cancels the upgrade.
    function showDialog(nodeName, items, remembered) {
        return new Promise(function(resolve) {
            var byFile = {};
            var order = [];
            items.forEach(function(it, idx) {
                it._idx = idx;
                if (!byFile[it.file]) { byFile[it.file] = []; order.push(it.file); }
                byFile[it.file].push(it);
            });

            var body = order.map(function(file) {
                var list = byFile[file];
                return '<details class="cu-file" data-file="' + esc(file) + '"' + (list.length <= 15 ? ' open' : '') + ' style="margin-bottom:12px;">' +
                    '<summary style="cursor:pointer;display:flex;align-items:center;gap:8px;padding:6px 0;">' +
                        '<strong>' + esc(file) + '</strong>' +
                        '<span class="text-muted text-sm">' + list.length + (list.length === 1 ? ' difference' : ' differences') + '</span>' +
                        (CRITICAL[file] ? '<span class="badge badge-warning" style="font-size:11px;">network-critical</span>' : '') +
                        '<span style="margin-left:auto;display:flex;gap:6px;">' +
                            '<button type="button" class="btn btn-sm btn-secondary" data-bulk="mine">All mine</button>' +
                            '<button type="button" class="btn btn-sm btn-secondary" data-bulk="new">All new</button>' +
                        '</span>' +
                    '</summary>' +
                    list.map(function(it) { return renderItem(it, it._idx, remembered); }).join('') +
                '</details>';
            }).join('');

            var backdrop = document.createElement('div');
            backdrop.className = 'modal-backdrop active';
            backdrop.innerHTML =
                '<div class="modal-panel modal-panel-lg">' +
                    '<div class="modal-panel-header"><h3>Config differences &mdash; ' + esc(nodeName) + '</h3></div>' +
                    '<div class="modal-panel-body">' + body + '</div>' +
                    '<div style="display:flex;justify-content:flex-end;gap:8px;padding:12px 20px;border-top:1px solid var(--border);">' +
                        '<button type="button" class="btn btn-secondary" data-action="cancel">Cancel upgrade</button>' +
                        '<button type="button" class="btn btn-primary" data-action="ok">Continue</button>' +
                    '</div>' +
                '</div>';
            document.body.appendChild(backdrop);

            function close(result) {
                backdrop.remove();
                resolve(result);
            }

            backdrop.addEventListener('click', function(ev) {
                var t = ev.target;
                if (!(t instanceof Element)) return;
                var bulk = t.getAttribute('data-bulk');
                if (bulk) {
                    ev.preventDefault();
                    ev.stopPropagation();
                    t.closest('details').querySelectorAll('input[type=radio][value=' + bulk + ']').forEach(function(r) { r.checked = true; });
                    return;
                }
                var action = t.getAttribute('data-action');
                if (action === 'cancel') {
                    close(null);
                } else if (action === 'ok') {
                    var keep = [];
                    items.forEach(function(it) {
                        var mine = backdrop.querySelector('input[name="cu-' + it._idx + '"][value="mine"]').checked;
                        remembered[memoKey(it)] = mine;
                        if (mine) keep.push(it.id);
                    });
                    close(keep);
                }
            });
        });
    }

    async function review(nodes, network) {
        var out = { cancelled: false, keep: {}, unsupported: [] };
        var remembered = {};

        for (var i = 0; i < nodes.length; i++) {
            var n = nodes[i];
            var r = await fetchPreview(n.id, network);
            if (r.unsupported) {
                out.unsupported.push(n.id);
                continue;
            }
            if (r.error) {
                if (!confirm('Could not compare the config of "' + n.name + '": ' + r.error +
                        '\n\nContinue without keeping custom values for this node?')) {
                    out.cancelled = true;
                    return out;
                }
                continue;
            }

            var items = (r.preview && r.preview.items) || [];
            if (items.length === 0) continue;

            var undecided = items.filter(function(it) { return !(memoKey(it) in remembered); });
            var picks;
            if (undecided.length === 0) {
                picks = items.filter(function(it) { return remembered[memoKey(it)]; }).map(function(it) { return it.id; });
            } else {
                picks = await showDialog(n.name, items, remembered);
                if (picks === null) {
                    out.cancelled = true;
                    return out;
                }
            }
            if (picks.length > 0) out.keep[n.id] = picks;
        }
        return out;
    }

    window.ConfigUpgrade = { review: review };
})();
