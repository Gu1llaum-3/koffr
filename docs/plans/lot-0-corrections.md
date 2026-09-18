# Plan lot 0 — Corrections de recette

> Statut : **validé par le propriétaire le 2026-09-18**. Exécuté par `/executer-plan`. Les règles communes à tous les plans sont
> dans `METHODE.md` § « Exécution d'un plan » et ne sont pas répétées ici.

Travail transverse issu de la session de recette du lot 0 (2026-09-18, instance Multipass `koffr`).
Il ne rouvre aucune exigence : il corrige ce que la recette a montré, et câble ce que le lot 0 avait
livré sans surface d'usage.

## Périmètre

**Les sept anomalies `A-01` à `A-07`**, toutes tranchées par le propriétaire le 2026-09-18. Le
registre fait foi : `docs/recette/anomalies.md`.

| `A-nn` | Gravité | Décision retenue |
| --- | --- | --- |
| `A-01` | **bloquant** | `-race` conditionnel en local, toujours bloquant en CI |
| `A-02` | gênant | Corriger le scénario de recette (`build` ≠ `release`) |
| `A-03` | gênant | Livrer `examples/koffr.yaml`, gardé valide par un test |
| `A-04` | cosmétique | Corriger le scénario (`--config`, `N-16`) |
| `A-05` | gênant | Nommer la section, pas le type Go |
| `A-06` | gênant | `config validate` résout les secrets ; `--offline` pour la forme seule |
| `A-07` | gênant | Câbler `obs` dans la racine cobra |

**Exigences touchées** — aucune n'est rouverte ; trois sont **mieux** tenues qu'avant :

| `E-nnn` | Ce que la correction change |
| --- | --- |
| `E-032` | Le message de clé inconnue cesse d'exposer un nom de type Go (`A-05`) |
| `E-033` | `config validate` constate réellement qu'un `*_file` est introuvable (`A-06`) |
| `E-121` | Les journaux JSON et le fichier avec rotation ont enfin un émetteur (`A-07`) |

**Écarté de ce plan** :

- `E-034` (joignabilité des bases, existence des outils) reste au **lot 1**. `A-06` ne fait résoudre
  que les secrets, ce qui est local : aucune connexion n'est ouverte.
- Le câblage de `internal/state` et `internal/egress` reste **hors périmètre** : le premier appelle
  `serve` (lot 5), le second un émetteur réel (lots 1, 6 et 8). `A-07` ne concerne que `obs`.

**Critère de sortie** :

1. `mise run verify` **vert sur l'instance Multipass `koffr`**, machine sans compilateur C, sans
   qu'on y installe quoi que ce soit ;
2. la CI reste **bloquante sur `-race`** — vérifié en lisant son journal, pas supposé ;
3. `koffr config validate` sur `examples/koffr.yaml` → accepté ; le même avec un `password_file`
   absent → **refusé**, et `--offline` → accepté ;
4. une clé inconnue nomme **la section, la clé et la ligne**, sans nom de type Go ;
5. `koffr version` écrit une ligne JSON dans le fichier de journal de `E-026` ;
6. le scénario `docs/recette/lot-0-scenario.md` est rejouable **mot à mot**, sans contournement.

## État de départ (vérifié le 2026-09-18)

- Lot 0 terminé : sept vagues mergées, `main` vert en local et en CI au commit `044de93`.
- Recette jouée sur Ubuntu 26.04 LTS `arm64`, `mise` 2026.9.11, **sans Go ni Docker préinstallés**.
  Instance réinitialisable par snapshot.
- `mise run verify` y échoue à l'étape `test` : `-race requires cgo`. Aucun `gcc`, donc
  `go env CGO_ENABLED` vaut `0`. **Sans `-race`, les 7 paquets passent.**
- `internal/obs`, `internal/state` et `internal/egress` ne sont importés par aucun paquet hors du
  leur : vérifié par `grep` sur `cmd/` et `internal/cli/`.
- `internal/cli/config.go` appelle `config.Parse` et **jamais** `config.Load` ; `Load` n'a donc
  aucun appelant en dehors de ses tests.
- Il n'existe aucun répertoire `examples/`.

## Ce que le cahier des charges dit, et ce qu'il ne dit pas

- **Dit** : § 5.1 `F1.3` — `config validate` vérifie « la syntaxe, la cohérence, la joignabilité des
  bases et l'existence des outils, **sans rien exécuter d'autre** ». Résoudre un secret local entre
  dans « la cohérence » et n'exécute rien ; c'est `A-06`.
- **Ne dit pas** : rien sur un mode hors ligne de `config validate` → `N-1` ci-dessous, annoncé à la
  recette. Rien sur le niveau de journal par défaut → `N-3`.
- **Contredit** : rien ici.

## Décisions d'implémentation

- **N-1 `config validate` résout les secrets ; `--offline` ne les résout pas.** *Raison* : la
  commande faite pour vérifier une configuration doit constater ce qu'un exploitant croit qu'elle
  constate ; mais relire une configuration de production depuis un poste reste légitime. *Exclut* :
  deux commandes séparées, et un `validate` qui ouvrirait une connexion — `E-034` reste au lot 1.
- **N-2 `-race` est conditionné à la présence d'un compilateur C, en local seulement.** La tâche
  `test` interroge `go env CGO_ENABLED` : à `1` elle lance `-race`, à `0` elle lance les tests sans
  et **écrit pourquoi**. La CI exporte `KOFFR_REQUIRE_RACE=1`, qui fait **échouer** la tâche si le
  détecteur n'est pas disponible. *Raison* : ne bloquer personne sur une machine nue sans perdre la
  garantie là où elle est mesurée. *Exclut* : retirer `-race`, et l'exiger partout.
- **N-3 Le logger est construit par la racine cobra et passé par le contexte.** Niveau `info` par
  défaut, `--log-level` pour le changer, fichier `E-026` écrit **seulement si son répertoire est
  accessible en écriture** — sinon la sortie standard seule, sans échec. *Raison* : `koffr version`
  lancé par un utilisateur non privilégié ne doit pas échouer sur `/var/log/koffr`. *Exclut* : un
  logger global, et un échec de démarrage pour un journal.
- **N-4 `examples/koffr.yaml` est la source, `internal/config/testdata/reference.yaml` en devient
  une copie vérifiée.** Un test compare les deux et échoue si elles divergent. *Raison* : un
  exemple qui périme est pire que pas d'exemple. *Exclut* : deux fichiers entretenus à la main.
- **N-5 La traduction des erreurs de `yaml.v3` se fait par une table type Go → nom de section.**
  *Raison* : `yaml.v3` ne donne que le nom du type Go ; il n'y a pas d'autre accroche. *Exclut* :
  réécrire l'analyse syntaxique pour produire nos propres erreurs, disproportionné ici.

## Vagues

### Vague 1 — `verify` passe sur une machine nue (`lot0/wave-8-race-when-available`) — `A-01`

- [x] **1.1** `mise.toml` : la tâche `test` détecte le compilateur C (`go env CGO_ENABLED`) et
      choisit `-race` ou non, **en journalisant son choix**. `KOFFR_REQUIRE_RACE=1` fait échouer la
      tâche si `-race` est indisponible (`N-2`). *Pas de test unitaire : tâche d'outillage,
      justifié ; la vérification est l'exécution sur l'instance.*
- [x] **1.2** `.github/workflows/verify.yml` : exporter `KOFFR_REQUIRE_RACE=1`.
- [x] **1.3** `CLAUDE.md` § Commandes et § Conventions : dire le comportement et pourquoi.
- [x] **1.4** **Vérifié sur l'instance Multipass** : `mise run verify` vert sans rien y installer,
      et le message annonce que `-race` est sauté. Puis, après `apt install -y gcc`, `-race`
      s'active — l'instance est ensuite restaurée par snapshot.
- [x] **1.5** Vague verte : `verify`, commit `fix(tooling): run the race detector when a C toolchain is there`.

### Vague 2 — La configuration dit la vérité (`lot0/wave-9-validate-and-messages`) — `A-05`, `A-06`, `A-03`

- [ ] **2.1** Test d'abord `internal/config/parse_test.go` — **`CFG-01` amendée** : le message nomme
      **la section** (`agent`, `databases[0]`, `destinations[0]`), la clé et la ligne, et **ne
      contient plus** `config.` ni `type `. Puis la table de traduction (`N-5`).
- [ ] **2.2** Test d'abord `internal/cli/config_test.go` — **`CFG-09`** : `config validate` échoue
      sur un `password_file` absent et sur un `*_env` non défini, en nommant le fichier ou la
      variable ; `--offline` accepte les deux. Puis le code (`N-1`).
- [ ] **2.3** `examples/koffr.yaml` créé depuis le § 5.1, et test **`CFG-10`** : l'exemple est
      accepté, et il est **identique** à `internal/config/testdata/reference.yaml` (`N-4`).
      `README.md` le pointe.
- [ ] **2.4** `internal/config/rules.md` : `CFG-01` amendée, `CFG-09` et `CFG-10` ajoutées avec leur
      source ; la divergence « `validate` ne résout aucun secret » est **barrée**, pas supprimée.
- [ ] **2.5** Vague verte : `verify`, commit `feat(config): validate resolves secrets and errors name the section`.

### Vague 3 — Le journal a un émetteur (`lot0/wave-10-wire-logging`) — `A-07`

- [ ] **3.1** Test d'abord `internal/cli/logging_test.go` — la racine construit le logger, une
      commande écrit **une ligne JSON**, `--log-level` change le niveau, et **aucun secret** n'y
      apparaît.
- [ ] **3.2** Test — un répertoire de journal non accessible en écriture **ne fait pas échouer** la
      commande : la sortie standard suffit (`N-3`).
- [ ] **3.3** Le code : `internal/cli` construit `obs`, le passe par le contexte, `cmd/koffr` le
      ferme proprement.
- [ ] **3.4** Mesurer à nouveau le binaire : `obs` et `lumberjack` y entrent. Noter l'écart.
- [ ] **3.5** Vague verte : `verify`, commit `feat(cli): wire structured logging into every command`.

### Vague 4 — Le scénario redevient jouable (`lot0/wave-11-acceptance-scenario`) — `A-02`, `A-04`

- [ ] **4.1** `docs/recette/lot-0-scenario.md` : `mise run release` pour les trois cibles,
      `--config` au lieu de `--file`, `examples/koffr.yaml` au lieu de `exemple/koffr.yaml`,
      parcours 4.3 réécrit avec le journal réel, parcours 3.5 avec le nouveau comportement.
      Prérequis corrigés : Docker **n'est pas** nécessaire au lot 0.
- [ ] **4.2** `docs/recette/anomalies.md` : colonne « Corrigée » remplie pour les sept.
- [ ] **4.3** **Rejouer le scénario entier sur l'instance Multipass restaurée par snapshot**, mot à
      mot, sans contournement. Consigner le résultat.
- [ ] **4.4** Vague verte : `verify`, commit `docs(recette): replay the lot 0 scenario as written`.

## Vérification de bout en bout

Toute sur l'instance Multipass `koffr`, restaurée par snapshot, dépôt cloné depuis `main` :

1. `mise install` puis `mise run verify` → **vert**, sans rien installer à la main ;
2. le journal de la CI montre `-race` actif et la tâche échouerait sans lui ;
3. `koffr config validate --config examples/koffr.yaml` → accepté ;
4. le même fichier avec `password_file: /nexiste/pas` → **refusé** en nommant le fichier ;
   avec `--offline` → accepté ;
5. `timezon:` → refusé en nommant `agent`, `timezon` et la ligne, **sans** `config.Agent` ;
6. `koffr version --state-dir /tmp/e --log-dir /tmp/e` → une ligne JSON dans le fichier de journal ;
7. les six parcours du scénario joués **mot à mot**.

## Recette

- Scénario : `docs/recette/lot-0-scenario.md`, corrigé par la vague 4 et **rejoué en entier**.
- **Décision attendue** : `D-01` reste ouverte — l'instance Multipass répond-elle définitivement à
  « qui joue la recette et sur quel parc » ? Le lot 1 exigera Docker et de vraies bases.
- **Ce qui est voulu et pourrait passer pour un bug** : `config validate` ne teste **toujours pas**
  la joignabilité des bases ni l'existence des outils (`E-034`, lot 1) ; `internal/state` et
  `internal/egress` restent sans appelant.

## Risques et points à vérifier en route

| Risque | Ce qu'on fait s'il se réalise |
| --- | --- |
| La détection du compilateur C par `go env CGO_ENABLED` se révèle fragile (croisement `GOOS`, variable déjà positionnée) | On teste explicitement la présence d'un `cc` dans le `PATH` plutôt que de déduire, et on l'écrit en `N-n` |
| Résoudre les secrets dans `validate` casse un usage légitime qu'on n'a pas vu | `--offline` est précisément là pour ça ; s'il ne suffit pas, on inverse le défaut par une `N-n` datée |
| Le fichier de journal par défaut (`/var/log/koffr`) n'est pas accessible en développement | Traité par `N-3` : sortie standard seule, sans échec. À vérifier explicitement en `3.2` |
| Câbler `obs` fait entrer `lumberjack` dans le binaire et rapproche du seuil | Mesuré en `3.4`. La marge est de 22,6 Mio : aucun risque réel à ce stade, mais le chiffre se note |
| Le scénario rejoué révèle de **nouvelles** anomalies | Elles sont numérotées `A-08` et suivantes, et on décide alors — on ne les corrige pas dans la foulée |

## Journal d'exécution

Rempli par `/executer-plan` : échecs, décisions `N-n` ajoutées en route, écarts au plan, datés.

### 2026-09-18 — vague 1, `A-01`

- `scripts/run-tests.sh` : `-race` si la machine peut le lancer, sauté **en le disant** sinon,
  **échec** si `KOFFR_REQUIRE_RACE=1`. La CI pose cette variable au niveau du job.
- **Le risque du plan s'est confirmé avant même d'être rencontré** : tester `go env CGO_ENABLED`
  ne suffit pas, la variable pouvant être exportée à la main sur une machine sans compilateur. Le
  script vérifie **en plus** que `go env CC` est trouvable dans le `PATH`. Pas de `N-n`
  supplémentaire : le plan prévoyait déjà ce geste dans sa table des risques.
- Les trois chemins exercés localement : `-race` actif (`clang`), sauté, et exigé-absent → **code
  de retour 1**.
- **Vérifié sur l'instance Multipass nue**, sans rien y installer : `mise run verify` → **code de
  retour 0**, en 3,1 s, avec le message qui explique pourquoi `-race` est sauté et comment
  l'obtenir. C'est le critère de sortie n° 1 du plan, atteint.
- Puis, après `apt-get install build-essential` sur la même instance : `CGO_ENABLED` repasse à `1`,
  le message devient `race detector: on (gcc)`, et les tests passent **avec** le détecteur.
  L'instance a ensuite été **restaurée par snapshot** — `gcc` et le clone ont disparu, l'état est
  celui du départ.
- `mise run verify` en local : **code de retour 0**.
