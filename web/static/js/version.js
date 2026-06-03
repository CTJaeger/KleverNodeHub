// Klever node Docker tag parsing and comparison.
//
// Klever's tagging convention:
//   vX.Y.Z-N      stable build iteration N (-0 = first release of X.Y.Z, -1 = hotfix rebuild)
//   vX.Y.Z-rcN    release candidate N
//   val-...       validator-only track, otherwise same rules
//   latest        floating tag, ignored
//
// Stable beats RC for the same X.Y.Z — once vX.Y.Z-0 is published, all
// vX.Y.Z-rcN are older. Across X.Y.Z, higher version wins regardless.
// Git-hash suffixes (-g<hex>) are stripped before comparison.

(function (root) {
    function parseNodeTag(tag) {
        if (!tag) return null;
        var s = tag;
        if (s.indexOf('val-') === 0) s = s.slice(4);
        if (s === 'latest') return null;
        if (s.charAt(0) === 'v') s = s.slice(1);
        s = s.replace(/-g[0-9a-f]+$/, '');
        var m = s.match(/^(\d+)\.(\d+)\.(\d+)(?:-(.+))?$/);
        if (!m) return null;
        var suffix = m[4] || '';
        var stable = true;
        var iteration = 0;
        if (suffix === '') {
            stable = true;
            iteration = 0;
        } else if (/^\d+$/.test(suffix)) {
            stable = true;
            iteration = +suffix;
        } else {
            var rc = suffix.match(/^rc(\d+)$/i);
            if (!rc) return null;
            stable = false;
            iteration = +rc[1];
        }
        return { major: +m[1], minor: +m[2], patch: +m[3], stable: stable, iteration: iteration };
    }

    // >0 if a is newer than b, <0 if older, 0 if equal precedence.
    // Unparsable tags sort below parsable ones.
    function compareNodeTags(a, b) {
        var pa = parseNodeTag(a);
        var pb = parseNodeTag(b);
        if (!pa && !pb) return 0;
        if (!pa) return -1;
        if (!pb) return 1;
        if (pa.major !== pb.major) return pa.major - pb.major;
        if (pa.minor !== pb.minor) return pa.minor - pb.minor;
        if (pa.patch !== pb.patch) return pa.patch - pb.patch;
        if (pa.stable !== pb.stable) return pa.stable ? 1 : -1;
        return pa.iteration - pb.iteration;
    }

    // Highest-precedence tag on the given track (val-only vs non-val).
    // Prefers a stable release; falls back to the highest RC only if no
    // stable exists on the track at all (very early in a release cycle).
    function latestNodeTag(tags, valOnly) {
        var candidates = tags.filter(function (t) {
            if (t === 'latest' || t === 'val-latest') return false;
            var isVal = t.indexOf('val-') === 0;
            return valOnly ? isVal : !isVal;
        });
        if (!candidates.length) return '';
        var stables = candidates.filter(function (t) {
            var p = parseNodeTag(t);
            return p && p.stable;
        });
        var pool = stables.length ? stables : candidates;
        var best = '';
        pool.forEach(function (t) {
            if (!best || compareNodeTags(t, best) > 0) best = t;
        });
        return best;
    }

    // Sort copy of `tags` by precedence, newest first. Unparsable tags go last.
    function sortNodeTagsDesc(tags) {
        return tags.slice().sort(function (a, b) {
            return compareNodeTags(b, a);
        });
    }

    root.KleverVersion = {
        parse: parseNodeTag,
        compare: compareNodeTags,
        latest: latestNodeTag,
        sortDesc: sortNodeTagsDesc
    };
})(window);
