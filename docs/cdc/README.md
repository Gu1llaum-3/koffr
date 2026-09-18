# Cahier des charges

- Le document reçu, **tel quel**, non modifié : `<nom-original>.pdf|.docx|.md`, avec sa version et
  sa date dans le nom si elles n'y sont pas. Si le document est confidentiel, il est gitignoré et
  son chemin est dans `CLAUDE.local.md`.
- `analyse.md` : la lecture qu'on en fait (périmètre, acteurs, objets, flux, intégrations,
  silences, contradictions). Produite par `/demarrer-projet`, relue par le propriétaire.
- `exigences.md` : le registre `E-nn`. **C'est la table de traçabilité du projet** : chaque
  exigence, son § source, son type, son lot, son état. Un lot, un plan, une règle `MOD-nn` et un
  test citent leur `E-nn` ; le critère de sortie du dernier lot est « toutes les `E-nn` sont
  couvertes ou renvoyées par une décision écrite ».

## Format d'`exigences.md`

| #    | Exigence (reformulée, une phrase, vérifiable) | Source (§, page) | Type | Priorité | Lot | État |
| ---- | --------------------------------------------- | ---------------- | ---- | -------- | --- | ---- |

- **Type** : `fonctionnel`, `donnée`, `intégration`, `technique`, `sécurité`, `exploitation`,
  `contrainte` (délai, budget, réglementaire), `hors périmètre` (le CDC le dit explicitement).
- **Priorité** : celle du CDC si elle existe (`doit`, `devrait`, `pourrait`), sinon `à qualifier`
  et une `Q-nn`.
- **État** : `à faire`, `en cours (lot N)`, `couverte (MOD-nn, test)`, `reportée (B-nn)`,
  `écartée (D-nn / ADR)`, `en question (Q-nn)`.

Une exigence ambiguë n'est pas interprétée : elle est reformulée au plus près du texte et une
`Q-nn` est ouverte. Une exigence qui en cache plusieurs est scindée (`E-12a`, `E-12b`) avant
d'être affectée à un lot.
