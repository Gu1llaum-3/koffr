# Backlog — registre `B-nn`

Ce qu'on a vu pendant le travail et qu'on **ne fait pas maintenant** : améliorations, idées,
demandes hors cahier des charges, exigences reportées après la mise en service. Rien d'ici n'entre
dans un lot sans une décision écrite (`D-nn` tranchée ou ADR).

Chaque entrée dit d'où elle vient, ce qu'elle apporterait et ce qu'elle coûterait.

| Ce qui va ici                                        | Où va le reste                            |
| ---------------------------------------------------- | ----------------------------------------- |
| Une idée, une amélioration, une demande d'utilisateur | —                                         |
| Une exigence `E-nnn` reportée après la mise en service | — (garder le lien `E-nnn`)               |
| Un bug                                               | `recette/anomalies.md` ou un plan         |
| Un arbitrage                                         | `decisions.md`                            |

## Registre

| #    | Idée | Origine | Apport | Coût estimé | État |
| ---- | ---- | ------- | ------ | ----------- | ---- |
| B-01 | Sauvegarde des objets globaux d'un cluster (rôles, tablespaces, extensions) et restauration des propriétaires et des droits | `Q-07`, hypothèse retenue au lot 2 | Une restauration qui rend l'instance telle qu'elle était, pas seulement ses lignes | Une entrée de configuration de plus, un second artefact par cluster | ouvert |
| B-02 | Point d'entrée de métriques (Prometheus) et sonde de santé de l'agent | `analyse.md` § 7, silence 15 | Supervision d'un parc par les outils déjà en place chez l'exploitant, sans serveur central | Faible : un `/metrics` servi par `httpd` | ouvert |
| B-03 | Reprise d'un dépôt d'archives existant (Portabase ou autre) : inventaire, catalogage, restauration | `Q-22`, hypothèse retenue (aucune reprise) | Migration sans faire tourner deux outils en parallèle | Élevé : un lot entier, un format tiers à lire | ouvert |
| B-04 | Sauvegarde de plusieurs bases d'une même instance par une seule entrée de configuration | `Q-07` | Moins de configuration sur un serveur qui héberge vingt bases | Moyen : boucle de jobs, rétention par base | ouvert |
| B-08 | Installation gérée d'outils (`koffr tools install`), `E-043` à `E-045` et `E-131` | ADR-0014 : sortie du MVP, faute d'engagement tenable sur la construction et l'hébergement | Un agent qui se débrouille seul quand la distribution ne livre pas le client voulu ; les scénarios 2 et 3 du § 8 | Élevé et **récurrent** : 10 binaires × 2 architectures, signés, hébergés, refaits à chaque version amont. La technique est déjà prouvée (`docs/inputs/spike-2026-09-rpath.md`) | ouvert |
| B-07 | Scinder `CLAUDE.md` (269 lignes) et `ROADMAP.md` (217 lignes), dont le seuil de relecture est 150 | Rétro du lot 0 | Deux documents que quelqu'un relit vraiment, au lieu de deux qu'on survole | Faible : sortir le glossaire dans `docs/glossaire.md`, et les paragraphes de périmètre des lots dans leur plan | ouvert |
| B-06 | `koffr config show` imprime les champs non renseignés (`host: ""`, `port: 0`) | Vague 4 du lot 0 : la sortie du § 5.1 fait 130 lignes dont la moitié est vide | Une sortie qu'un exploitant relit d'un coup d'œil et colle dans un ticket | Faible : `omitempty` sur les champs optionnels, en prenant garde à ne pas masquer un `false` ou un `0` voulus | ouvert |
| B-09 | Laisser `pg_dump` compresser lui-même en zstd (`--compress=zstd:3`) au lieu de le faire dans le pipeline, pour les bases PostgreSQL 16+ | Question du propriétaire, 2026-09-22, après le correctif `--compress=0` de la vague 6 | **Mesuré** sur une base de 107 Mo (400 000 lignes, texte libre) : 6,69 Mo en **0,4 s** contre 7,25 Mo en 0,7 s pour notre chaîne — **8 % de moins en deux fois moins de temps**, parce que `pg_dump` compresse ses blocs en parallèle | Moyen, et surtout **ce que ça casse** : `mysqldump` n'a pas d'équivalent, donc `pipeline`, `size_raw` et le raisonnement du § 4.5 voudraient dire deux choses selon le moteur. Suppose aussi un `pg_dump` construit avec zstd, ce qui n'est pas garanti. Demande un ADR : le § 5.3 fixe `--compress=0` dans le manifeste | ouvert |
| B-05 | Analyse statique des scripts shell (`shellcheck`) dans `verify` | Vague 2 du lot 0 : `scripts/spike-rpath.sh` est le premier script du dépôt, et rien ne le vérifie | Un script d'exploitation cassé se voit en CI, pas sur la machine d'un utilisateur | Faible : un outil de plus dans `mise.toml` et une tâche | ouvert |

## Reporté après la mise en service

Les exigences du cahier des charges que la roadmap place après la première mise en service, avec
leur `E-nnn` et le § source. Elles ne sont pas oubliées : elles attendent une V2.

| `E-nnn` | Ce qui est reporté | Source | Ce que le CDC en dit |
| ------- | ------------------ | ------ | -------------------- |
| `E-018` | Moteurs MongoDB, SQLite, Redis, Valkey, SQL Server, volumes | § 3 | Reporté ; le § 10 précise que le dumper natif est « trivial et sans risque » pour SQLite, Redis et Valkey — c'est par là qu'on rouvrira |
| `E-019` | Destinations Azure Blob, GCS, Google Drive, rclone | § 3 | Reporté sans condition |
| `E-020` | `kubectl exec` et dumpers natifs | § 3, § 10 | Reporté ; `kubectl exec` est le pendant naturel de la stratégie `exec` |
| `E-021` | Restauration de test dans une base jetable | § 3, § 10 | « La vérification qui a le plus de valeur », mais suppose de provisionner une instance jetable → v2, en option |
| `E-022` | Intégrations natives Slack, Discord, PagerDuty, SMS | § 3 | Le webhook générique les couvre ; à rouvrir seulement si un utilisateur le demande |
| `E-023` | Authentification multi-utilisateur et RBAC | § 3, § 10 | Appartient au serveur central, donc à son propre cahier des charges |
| `E-125` | Dumpers natifs en Go | § 10 | « À rouvrir pour SQLite, Redis et Valkey » |
| `E-126` | Restauration de test automatique | § 10 | « À concevoir en v2, comme option » |

Les cinq autres exigences hors périmètre — `E-001` à `E-004` (non-objectifs du § 1.3), `E-127`
(PITR et sauvegarde physique), `E-128` (multi-utilisateur) et `E-129` (déduplication, écartée par
ADR-0005) — ne sont pas reportées mais **écartées du produit**. Elles restent au registre avec leur
raison ; les rouvrir demande un nouvel ADR, pas une ligne de backlog.
