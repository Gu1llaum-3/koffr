# Anomalies de recette — registre `A-nn`

| #    | Session | Écran / parcours | Constat | Gravité | Décision | Corrigée (plan, commit) |
| ---- | ------- | ---------------- | ------- | ------- | -------- | ----------------------- |
| A-01 | lot 0, 2026-09-18 | 1.2 — `mise run verify` | Sur une machine neuve conforme aux prérequis (Ubuntu 26.04 arm64, `mise`, **sans Go**), `verify` **échoue** : `go test ./... -race` répond `-race requires cgo; enable cgo by setting CGO_ENABLED=1`. Aucun compilateur C n'est installé, donc Go force `CGO_ENABLED=0`. Sans `-race`, les 7 paquets passent. Passe en CI (les runners GitHub ont `gcc`) et sur le poste de développement (macOS, Xcode CLT). | **bloquant** | `-race` **conditionnel** : lancé si un compilateur C est présent, sinon les tests sans, en l'annonçant. La CI garde `-race` toujours bloquant | `lot0/wave-8-race-when-available` (PR #7) |
| A-02 | lot 0, 2026-09-18 | 2.1 — les trois cibles | Le scénario dit « `mise run build` → trois binaires ». `mise run build` n'en produit **qu'un**, pour la plateforme hôte. Les trois cibles d'ADR-0011 sont produites par `mise run release`, qui fonctionne (4,0 / 3,8 / 3,9 Mio, aucun binaire Windows). | gênant | Corriger le **scénario** : `build` = la machine, `release` = les trois cibles | `lot0/wave-11-acceptance-scenario` |
| A-03 | lot 0, 2026-09-18 | 3.1 — configuration d'exemple | Le scénario invoque `exemple/koffr.yaml`. **Ce fichier n'existe pas.** La seule configuration de référence livrée est `internal/config/testdata/reference.yaml`, un chemin de test qu'un exploitant n'a aucune raison de connaître. | gênant | Livrer **`examples/koffr.yaml`**, pointé par le README, avec un test qui le garde valide | `lot0/wave-9-validate-and-messages` (PR #8) |
| A-04 | lot 0, 2026-09-18 | 3.1 — nom du drapeau | Le scénario écrit `--file` ; l'implémentation expose `--config`, tranché par `N-16` du plan du lot 0. `--file` répond `unknown flag`. C'est le scénario qui est en retard sur la décision. | cosmétique | `N-16` : c'est `--config`. Corriger le **scénario** | `lot0/wave-11-acceptance-scenario` |
| A-05 | lot 0, 2026-09-18 | 3.2 — message de clé inconnue | Le message nomme bien la clé **et sa ligne** (`line 8: field timezon not found in type config.Agent`), donc `E-032` est tenue. Mais il expose un **nom de type Go** (`type config.Agent`) là où un exploitant attend le nom de la section (`agent`). C'est la friction concrète que la question « les messages sont-ils compréhensibles ? » cherchait. | gênant | **Nommer la section** (`agent`) au lieu du type Go, en gardant la clé et la ligne | `lot0/wave-9-validate-and-messages` (PR #8) |
| A-06 | lot 0, 2026-09-18 | 3.5 — secret introuvable | Le scénario attend que `config validate` signale **tout de suite** un `password_file` pointant vers un fichier absent. Il répond **`ok`**. `CFG-03` est bien tenue, mais par `Load`, qu'**aucune commande n'appelle** au lot 0 ; `config validate` utilise `Parse`, qui ne vérifie que la forme. Voulu et écrit (`internal/config/rules.md` § Divergences) — mais la commande faite pour vérifier une configuration ne vérifie pas ce que l'exploitant croit qu'elle vérifie. | gênant | `config validate` **résout les secrets** ; `--offline` garde la vérification de forme seule | `lot0/wave-9-validate-and-messages` (PR #8) |
| A-07 | lot 0, 2026-09-18 | 4.3 — fichier de journal | Le scénario demande de lancer `koffr version` puis de regarder le fichier de journal. **Aucune commande n'écrit de journal** : `internal/obs` est écrit et testé, mais n'est importé par aucun paquet hors du sien. Même constat pour `internal/state` et `internal/egress`. Parcours **injouable** en l'état. | gênant | **Câbler `obs` dans la racine** cobra, avec `--log-level` et le fichier de `E-026` | `lot0/wave-10-wire-logging` (PR #9) |

Toutes tranchées le **2026-09-18** par le propriétaire, en session interactive. Corrections
regroupées dans **`docs/plans/lot-0-corrections.md`**, exécuté avant `/cloturer-lot`.

Gravité : `bloquant` (empêche le parcours), `gênant` (contourne), `cosmétique`. Une anomalie qui
révèle un choix voulu n'est pas corrigée : elle renvoie à l'ADR ou au `N-n`, et on juge si
l'explication doit changer.

## Ce qui a été vérifié et qui passe

Session du 2026-09-18, instance Multipass `koffr` (Ubuntu 26.04 LTS, `arm64`, `mise` 2026.9.11,
**sans Go ni Docker préinstallés**), dépôt cloné depuis GitHub sur `main` au commit `044de93`.

| Parcours | Résultat |
| --- | --- |
| 1.1 `mise install` sur machine neuve | ✅ Go 1.27.1 et `golangci-lint` 2.13.2 apportés par `mise` en **5,5 s**, rien installé à la main |
| 1.2 `mise run verify` | ❌ `A-01`. Sans `-race` : `check`, `lint` (0 issue) et les 7 paquets de test passent, en **38 s** dépendances comprises |
| 1.3 CI verte sur `main` | ✅ `success` sur le commit exact qui a été cloné |
| 2.1 Trois cibles, aucun Windows | ✅ par `mise run release` (`A-02` sur le nom) |
| 2.2 Tailles sous 30 Mo | ✅ 4,0 / 3,8 / 3,9 Mio — **identiques au poste de développement**, la construction est reproductible |
| 2.3 `version --json` | ✅ six champs, aucun vide |
| 3.1 Forme cible acceptée | ✅ `ok … 2 databases, 3 destinations, 2 alert channels, timezone Europe/Paris` (`A-03`, `A-04` sur le chemin et le drapeau) |
| 3.2 `timezon:` refusé avec clé et ligne | ✅ code 1 (`A-05` sur la formulation) |
| 3.3 `agent.timezone` absent | ✅ `agent.timezone is required: koffr reads schedules in a zone you declare, never in the one of the host` |
| 3.4 Deux formes à la fois | ✅ `databases[0] boutique-prod: password and password_file are given together; keep one` |
| 3.5 `password_file` absent | ❌ `A-06` |
| 4.2 `config show --redact` | ✅ topologie complète (hôtes, ports, identifiants, bucket) ; **aucune** des quatre valeurs sensibles — littérale, par `_env`, par `_file` — n'apparaît ; les littérales sont marquées `[redacted]` |
| 4.3 Journal | ❌ `A-07` |
| 5 Spike | ✅ conclusion explicite et **positive** : `E-043` tenable, Alpine comprise. Aucun ADR d'amendement à écrire |
| 6.1 Garde-fou d'architecture | ✅ un import de `internal/state` dans `internal/domain/backup` fait échouer le contrôle en nommant **`AR-01`**. Via `mise run verify`, l'échec survient mais **pour la mauvaise raison** : `A-01` frappe à l'étape précédente |

**Écart d'environnement, sans effet sur ce lot** : l'instance n'a pas Docker, que le scénario
demande. Le lot 0 n'en a besoin que pour rejouer le spike (parcours 5 se contente de lire le
rapport). À installer avant la recette du lot 1.
