# ADR-0010 — Le domaine est découpé par cas d'usage, les composants du CDC sont des adaptateurs

- **Date** : 2026-09-18
- **Statut** : accepté
- **Exigences** : `E-024`, `E-025`, `E-027`, `E-066`, `E-098`, `E-017`, `E-105`
- **Références** : CDC § 4.2 ; lié à ADR-0001 (dépôts), ADR-0008 (sorties), ADR-0009 (droits)

## Contexte

Le § 4.2 donne neuf composants : `scheduler`, `resolver`, `engine`, `pipeline`, `store`, `catalog`,
`notifier`, `uplink`, `httpd`. C'est un découpage **par composant technique**, pas par cas d'usage :
`store` et `engine` sont des adaptateurs (trois destinations, trois moteurs), `pipeline` est une
mécanique, tandis que `resolver`, `catalog` et `scheduler` portent de vraies règles métier.

Le kit exige un découpage où le métier n'importe rien de l'infrastructure, et où chaque module porte
son `rules.md`. Il faut donc dire lequel des deux découpages fait autorité sur les frontières.

## Décision

Le **domaine est découpé par cas d'usage** ; les composants du § 4.2 qui sont des adaptateurs
restent des adaptateurs, derrière des interfaces déclarées par le domaine.

```
koffr/
  cmd/koffr/            # point d'entrée, câblage, cobra
  protocol/             # paquet PUBLIC : messages de liaison, règles de validation
  internal/
    config/             # CFG — analyse stricte du YAML, seul lecteur de l'environnement
    domain/             # métier pur : aucun import d'adaptateur, de réseau, de sous-process
      resolve/          # RSV — résolution des outils, matrice de compatibilité
      backup/           # BKP — cas d'usage de sauvegarde, tampon, manifeste
      verify/           # VRF — empreinte et structure
      catalog/          # CAT — index des archives et emplacements
      retention/        # RET — politique GFS et règles de sûreté
      restore/          # RST — contrôles préalables et modes de nettoyage
      schedule/         # SCH — échéances, rattrapage, plancher opposable
      alert/            # ALR — événements, anti-répétition, délégation
      crypto/           # CRY — destinataires age, règles de clé
      shared/           # erreurs typées, horodatage, tailles, contexte d'appel
    engine/             # adaptateurs postgresql, mysql, mariadb (sondes, sous-process)
    store/              # adaptateurs filesystem, s3, sftp (interface unique E-066)
    pipeline/           # zstd, age, empreinte, fichier tampon
    state/              # SQLite, migrations, requêtes
    egress/             # porte de sortie : smtp, webhook, liaison, téléchargement d'outils
    httpd/              # interface web embarquée, API JSON
    cli/                # commandes cobra
```

**Règles de dépendance, vérifiées par le lint et bloquantes** (`golangci-lint` avec `depguard` et
`forbidigo`) :

| Depuis | Interdit d'importer |
| --- | --- |
| `internal/domain/**` | `internal/engine`, `store`, `pipeline`, `state`, `egress`, `httpd`, `cli` |
| `internal/domain/**` | `net`, `net/http`, `net/smtp`, `os/exec`, `database/sql` |
| `internal/domain/<a>` | `internal/domain/<b>` (un module ne connaît pas son voisin ; seul `shared` est commun) |
| `internal/httpd/**`, `internal/cli/**` | `internal/state` (passer par un cas d'usage du domaine) |
| tout sauf `internal/config` | `os.Getenv` et la lecture du fichier de configuration |
| tout sauf `store`, `engine`, `egress` | l'ouverture d'une connexion réseau |
| tout sauf `engine` | `os/exec` |
| tout sauf `state` | `database/sql`, `modernc.org/sqlite` |
| `protocol/` | tout le reste du dépôt |

- Chaque module du domaine porte un `rules.md` (gabarit `docs/modeles/rules.md`) et un code `MOD`
  déclaré dans `ARCHITECTURE.md`.
- Le domaine déclare les **ports** dont il a besoin (`ToolFinder`, `Store`, `Clock`, `Notifier`) ;
  les adaptateurs les implémentent et sont câblés dans `cmd/koffr`.
- `protocol/` est public et n'importe rien : c'est ce que `koffr-server` importera (ADR-0001).

## Conséquences

- Les noms du § 4.2 sont conservés là où ils désignent un adaptateur (`engine`, `store`, `pipeline`,
  `httpd`) et deviennent des modules du domaine là où ils portent des règles (`resolver` → `resolve`,
  `catalog`, `scheduler` → `schedule`, `notifier` → `alert`). Le glossaire porte la correspondance.
- `un module ne connaît pas son voisin` est la règle la plus contraignante : la sauvegarde a besoin
  de la résolution, du catalogue et des alertes. Elle les reçoit par des ports déclarés chez elle,
  câblés dans `cmd/koffr`. C'est plus verbeux, et c'est ce qui rend le domaine testable sans base.
- Les règles ci-dessus ne valent que si elles sont **bloquantes au lot 0** : une règle d'architecture
  non vérifiée par le lint n'existe pas.
- **Ce qui rouvrirait la décision** : un module du domaine qui n'aurait aucune règle propre (il
  deviendrait un adaptateur), ou une mécanique de ports devenue plus coûteuse que le couplage évité.

## Alternatives écartées

- **Suivre le § 4.2 à la lettre** : `store` et `engine` deviendraient des modules métier avec un
  `rules.md`, alors qu'ils n'ont d'autre règle que « implémenter l'interface ».
- **Un paquet plat par couche** (`internal/service`, `internal/repository`) : les frontières
  deviennent horizontales, une modification de la rétention touche trois paquets.
- **`protocol/` sous `internal/`** : rendrait le paquet inimportable par `koffr-server`.
