-- 0018_domain_target_ports: a domain allowance may now say which of the two web
-- ports it covers, or that it covers neither.
--
-- 0010 forbade ports on a domain row, and the reason it gave was right at the
-- time: a domain resolves to addresses the rule engine never sees, so there was
-- nowhere in a `pass tls`/`pass http` rule to put a port. That is no longer true
-- -- those rules take a port in their header, and the ruleset needs it:
--
--   ''         both web ports, which is what every existing row means
--   '443'      https only
--   '80'       http only
--   'none'     the name resolves and nothing may leave
--
-- 'none' is the one that is new in kind rather than degree. A guest cannot reach
-- ssh on a hostname -- ssh carries no name for suricata to check a rule against,
-- so that access is granted by address, as an 'ip' row. But the guest still has
-- to be able to *resolve* the name, and the filtering resolver only answers what
-- a domain row lists. 'none' is how a user says "let this name resolve" without
-- also granting it the web.
--
-- transport stays forbidden on a domain row. It would have nothing to say: both
-- rules a domain compiles to are tcp by construction.
ALTER TABLE vm_network_targets DROP CONSTRAINT vm_network_targets_shape;

ALTER TABLE vm_network_targets ADD CONSTRAINT vm_network_targets_shape CHECK (
    (kind = 'domain' AND transport = '' AND ports IN ('', '80', '443', '80,443', 'none'))
    OR
    (kind = 'ip' AND transport <> '')
);
