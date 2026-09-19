# Plan lot 1 — Corrections de recette

> Statut : **terminé le 2026-09-19**. Trois vagues mergées, `verify` et CI verts, scénario rejoué
> en entier sur l'instance. Reste `/cloturer-lot`. Exécuté par `/executer-plan`. Les règles communes à tous les plans sont
> dans `METHODE.md` § « Exécution d'un plan » et ne sont pas répétées ici.

Travail transverse issu de la session de recette du lot 1 (2026-09-19, instance Multipass `koffr`,
parc réel de trois familles). Il ne rouvre aucune exigence : il corrige ce qu'une vraie machine a
montré, et branche ce que le lot 1 avait livré sans surface d'usage.

## Périmètre

**Les trois anomalies `A-09` à `A-11`**, toutes **bloquantes**. Le registre fait foi :
`docs/recette/anomalies.md`.

| `A-nn` | Constat | Ce qu'il faut |
| --- | --- | --- |
| `A-09` | koffr résout les liens symboliques, ce qui casse l'`argv[0]` du `pg_wrapper` de Debian ; puis il lit « 153 » dans `at … line 153` et en fait une version | Exécuter le chemin **tel qu'il a été trouvé**, et n'accepter une version que d'une réponse qui **nomme l'outil ou sa famille** |
| `A-10` | Les clients MySQL et MariaDB **ne peuvent pas coexister** sur Debian et Ubuntu (`Conflicts: virtual-mysql-client-core`) | Le dire dans le `README`, et désigner la stratégie `exec` comme la réponse — ADR-0015 |
| `A-11` | `doctor` répond `none` sur une base déclarant `strategy: exec` : le `ContainerFinder` n'est branché nulle part | Le brancher dans `doctor` et `config validate` |

**Exigences touchées** — aucune n'est rouverte ; deux sont **mieux** tenues qu'avant :

| `E-nnn` | Ce que la correction change |
| --- | --- |
| `E-013` | La troisième source — le conteneur — devient réellement utilisable, pas seulement implémentée (`A-11`) |
| `E-039` | La version cesse d'être devinable à partir d'un message d'erreur (`A-09`) |
| `E-046` | La stratégie `exec` a enfin un effet observable |

**Écarté de ce plan** :

- **Toute reprise d'ADR-0014.** L'installation gérée reste hors MVP. `A-10` ne la rouvre pas : elle
  montre que la stratégie `exec` en est le complément nécessaire, pas que l'installation gérée
  manque.
- **Les quatre champs restants de `doctor`** (`E-104b`) : lot 6, comme prévu.

**Critère de sortie** :

1. Sur l'instance de recette, `koffr tools list` rapporte `/usr/bin/pg_dump` avec sa **vraie**
   version, et **aucun candidat fantôme** ;
2. une sortie qui ne nomme ni l'outil ni sa famille est **écartée**, quel que soit le nombre qu'elle
   contient ;
3. une base déclarant `tools: { strategy: exec, container: … }` obtient de `doctor` l'outil du
   conteneur, avec la provenance `container` ;
4. le `README` dit qu'un parc mixte MySQL et MariaDB sur une même machine **exige** `exec`, et
   pourquoi ;
5. le scénario `docs/recette/lot-1-scenario.md` est rejoué **en entier**, sans contournement.

## État de départ (vérifié le 2026-09-19)

Constaté sur l'instance, pas supposé.

- **Lot 1 terminé** : six vagues mergées, `main` vert en local et en CI au commit `a6071bf`.
- **Instance reconstruite** par le propriétaire : Ubuntu 26.04 `arm64`, 8 Go, 4 cœurs, 25 Go,
  `mise` et Docker, **sans Go ni compilateur C**. `verify` y passe en 2 min 15 à froid.
- **Parc réel** : PostgreSQL 16.15, MariaDB 11.4.13, MySQL 8.4.11 en conteneurs, plus une base
  éteinte. Les trois familles sont correctement identifiées.
- **`/usr/bin/pg_dump` est un lien** vers `/usr/share/postgresql-common/pg_wrapper`. Appelé par son
  nom : `pg_dump (PostgreSQL) 18.6`. Appelé par le chemin résolu :
  `Can't exec "--version": No such file or directory at … line 153`.
- **`koffr tools list` rapporte aujourd'hui deux candidats PostgreSQL** : le vrai en 18.6, et
  `pg_wrapper` en **153.0**.
- **`apt` refuse d'installer les deux clients** : `mariadb-client-core : Conflicts:
  virtual-mysql-client-core`, puis `Unable to satisfy dependencies`.
- **`ContainerFinder` n'est importé par aucun paquet hors du sien** : vérifié par `grep` sur
  `cmd/` et `internal/cli/`.
- Le piège du § 5.2 est évité sur cette machine, et `E-041` a tenu sur un cas que la distribution a
  produit elle-même.

## Ce que le cahier des charges dit, et ce qu'il ne dit pas

- **Dit** : § 5.2 `F2.1` nomme les sources, `F2.2` exige la version **par exécution**, `F2.9` fait
  de `exec` une déclaration par base.
- **Ne dit pas** : ce qu'il advient d'un outil empaqueté derrière un wrapper qui dispatche sur
  `argv[0]` — le cas n'existe que sur Debian et ses dérivés → `N-1`. Ni ce qu'on fait quand la
  distribution **interdit** deux clients côte à côte → `N-2` et ADR-0015.
- **Contredit** : rien. `A-10` est une contrainte de distribution, pas une contradiction du CDC.

## Décisions d'implémentation

- **N-1 Un candidat s'exécute par le chemin où il a été trouvé, jamais par son chemin résolu.** La
  résolution des liens sert **seulement** à dédupliquer — deux chemins menant au même inœud sont un
  candidat, pas deux —, et le chemin exécuté et rapporté reste celui de la découverte. *Raison* :
  `pg_wrapper` choisit la version d'après son `argv[0]` ; résoudre le lien lui retire l'information
  dont il vit. *Exclut* : rapporter à l'exploitant un chemin qu'il ne reconnaîtrait pas.
- **N-2 Une réponse ne vaut version que si elle nomme l'outil ou sa famille.** `pg_dump`,
  `PostgreSQL`, `MariaDB`, `mysqldump`, `MySQL` : à défaut, le candidat est **écarté**, même si sa
  sortie contient des chiffres. *Raison* : `RSV-02` promet qu'un candidat qui répond n'importe quoi
  est écarté, et la recette a montré une version fabriquée à partir de `at … line 153`. *Exclut* :
  la déduction de la famille depuis le **nom** du binaire, qui est ce qui a transformé `pg_wrapper`
  en candidat PostgreSQL.
- **N-3 `Diagnose` reçoit deux énumérateurs, l'un pour l'hôte et l'autre pour les conteneurs**, et
  choisit selon ce que la base déclare. *Raison* : `E-046` fait de `exec` une décision **par base**,
  et le domaine doit pouvoir l'exprimer sans connaître Docker. *Exclut* : un énumérateur unique qui
  déciderait tout seul, et un domaine qui importerait l'adaptateur.
- **N-4 Une base en `exec` dont le conteneur ne répond pas est un échec qui nomme le conteneur**,
  jamais un repli silencieux sur un outil de l'hôte. *Raison* : `E-046` — l'exploitant a demandé
  l'outil du conteneur pour une raison. *Exclut* : une résolution qui « se débrouille ».

## Vagues

### Vague 1 — Lire les outils tels qu'ils sont (`lot1/wave-7-read-tools-as-they-are`) — `A-09`

- [x] **1.1** Test d'abord `internal/engine/discover_test.go` — **`RSV-02` amendée** : une réponse
      qui ne nomme ni l'outil ni sa famille est **écartée**, même si elle contient un nombre. Cas
      tiré de la recette : `Can't exec "--version": No such file or directory at … line 153`.
- [x] **1.2** Test — un candidat atteint par un **lien symbolique** est exécuté **par le chemin du
      lien** et rapporté sous ce chemin (`N-1`). Fixture : un lien vers un script qui répond
      différemment selon son `argv[0]`, comme le fait `pg_wrapper`.
- [x] **1.3** Test — deux chemins menant au **même binaire** restent **un** candidat : la
      déduplication survit au changement.
- [x] **1.4** Le code. `toolFamily` cesse de deviner depuis le nom du fichier (`N-2`).
- [x] **1.5** `internal/domain/resolve/rules.md` : `RSV-02` amendée, avec le cas réel en exemple.
- [x] **1.6** **Vérifié sur l'instance** : `koffr tools list` rapporte `/usr/bin/pg_dump` en 18.6 et
      **aucun** candidat en 153.0.
- [x] **1.7** Vague verte : `verify`, commit `fix(engine): run a tool by the path it was found at, and refuse a version nobody announced`.

### Vague 2 — La stratégie `exec` est branchée (`lot1/wave-8-wire-exec-strategy`) — `A-11`

- [x] **2.1** Test d'abord `internal/domain/resolve/diagnose_test.go` — une base qui déclare un
      conteneur est diagnostiquée avec l'énumérateur de conteneurs, une autre avec celui de l'hôte,
      **dans le même parc** (`N-3`).
- [x] **2.2** Test — un conteneur qui ne répond pas donne un échec qui **nomme le conteneur**, et
      **jamais** un repli sur un outil de l'hôte (`N-4`).
- [x] **2.3** Le code : `resolve.Subject` porte le conteneur, `Diagnose` reçoit les deux
      énumérateurs, `internal/cli` les câble.
- [x] **2.4** Test `internal/cli/doctor_test.go` — `doctor` affiche la provenance `container` pour
      une base en `exec`.
- [x] **2.5** `rules.md` : `RSV-10` amendée — la stratégie a désormais un effet observable.
- [x] **2.6** **Vérifié sur l'instance**, contre le conteneur MariaDB réel : `doctor` annonce
      l'outil du conteneur avec sa version.
- [x] **2.7** Vague verte : `verify`, commit `feat(cli): use the container tool for a database that asks for it`.

### Vague 3 — Le parc mixte est dit (`lot1/wave-9-mixed-fleet`) — `A-10`

- [x] **3.1** **ADR-0015** : un parc mixte MySQL et MariaDB sur une même machine exige la stratégie
      `exec`. Il **borne** ADR-0014 sans le rouvrir, cite le conflit de paquets constaté, et dit ce
      que cela impose à l'image conteneur du lot 7.
- [x] **3.2** `README.md` : le conflit de paquets, son message exact, et `exec` comme réponse.
- [x] **3.3** `docs/recette/lot-1-scenario.md` : le parcours 4 gagne le cas réel — `mysqldump` qui
      est celui de MariaDB — et le parcours 7 devient vérifiable.
- [x] **3.4** `docs/recette/anomalies.md` : colonne « Corrigée » remplie pour les trois.
- [x] **3.5** **Rejouer le scénario entier sur l'instance**, sans contournement. Consigner.
- [x] **3.6** Vague verte : `verify`, commit `docs: a mixed MySQL and MariaDB fleet needs the exec strategy`.

## Vérification de bout en bout

Sur l'instance de recette, avec son parc réel :

1. `koffr tools list` → `/usr/bin/pg_dump` en **18.6**, aucun candidat en 153.0 ;
2. un script qui répond `Can't exec … at line 153` → **absent** de la liste ;
3. `koffr doctor` sur la base `erp` déclarée en `exec` → provenance **`container`** ;
4. le même avec un conteneur arrêté → échec **nommant le conteneur** ;
5. les sept parcours du scénario joués **mot à mot**.

## Recette

- Scénario : `docs/recette/lot-1-scenario.md`, corrigé par la vague 3 et **rejoué en entier**.
- **Décision attendue** : aucune nouvelle. `D-01` est répondue, `D-06` tranchée, `Q-15` sans objet.
- **Ce qui est voulu et pourrait passer pour un bug** :
  - `E-042` ne donne toujours pas de commande de correction (ADR-0014) ;
  - `koffr tools install` n'existe toujours pas ;
  - une machine ne peut toujours pas porter les deux clients — c'est la distribution, pas koffr.

## Risques et points à vérifier en route

| Risque | Ce qu'on fait s'il se réalise |
| --- | --- |
| Ne plus résoudre les liens fait réapparaître des doublons que la déduplication attrapait | Traité par `N-1` : on déduplique **par** le chemin résolu, on exécute **par** le chemin trouvé. Vérifié en `1.3` |
| Exiger que la réponse nomme l'outil écarte un outil légitime dont la sortie est inhabituelle | On mesure sur les binaires réels de l'instance — PostgreSQL, MariaDB, MySQL — avant de figer la liste des marqueurs. S'il en manque un, c'est une `N-n` datée |
| Brancher l'énumérateur de conteneurs alourdit la signature de `Diagnose` | Assumé : `E-046` fait de `exec` une décision par base, et le domaine doit pouvoir l'exprimer. Deux ports valent mieux qu'un adaptateur importé |
| Le rejeu révèle de **nouvelles** anomalies | Elles sont numérotées `A-12` et suivantes, et on décide alors — on ne les corrige pas dans la foulée |

## Résultat

Les **trois** anomalies sont corrigées et **vérifiées sur l'instance**. Critère de sortie :

| # | Critère | État |
| --- | --- | --- |
| 1 | `tools list` rapporte `/usr/bin/pg_dump` à sa vraie version, aucun fantôme | ✅ 18.6, six candidats réels |
| 2 | Une sortie qui ne nomme rien est écartée | ✅ le faux wrapper disparaît de la liste |
| 3 | Une base en `exec` obtient l'outil de son conteneur | ✅ `mysqldump 8.4.11 container` |
| 4 | Le `README` dit le conflit de paquets et sa sortie | ✅ avec le message exact d'`apt` |
| 5 | Le scénario rejoué en entier, sans contournement | ✅ sept parcours |

**Deux fois, c'est l'instance qui a tranché, pas mes tests.** `A-09` a résisté à une première
correction qui passait sur mon poste : le message d'erreur du wrapper contient le mot `postgresql`,
parce que le chemin du wrapper le contient. La règle retenue — **un outil qui annonce sa version dit
son propre nom en premier** — vient de là.

## Journal d'exécution

Rempli par `/executer-plan` : échecs, décisions `N-n` ajoutées en route, écarts au plan, datés.

### 2026-09-19 — vague 1, `A-09`

- `N-1` appliquée : les liens sont résolus **pour dédupliquer**, jamais pour exécuter. Un candidat
  atteint par un lien est lancé et rapporté **sous le chemin où il a été trouvé**.
- `N-2` appliquée, puis **renforcée en cours de route** : ma première version exigeait que la
  réponse **contienne** un marqueur de famille. L'instance l'a défaite immédiatement — le message
  d'erreur du wrapper cite `/usr/share/postgresql-common/pg_wrapper`, **qui contient le mot
  `postgresql`**. Le fantôme 153.0 est réapparu.
- **Règle retenue** : un outil qui annonce sa version **dit son propre nom en premier**
  (`pg_dump (PostgreSQL) 18.6`, `mariadb-dump from …`, `mysqldump  Ver …`), une erreur non. La
  famille doit **en plus** être nommée. Le nom du fichier n'a plus aucune voix — c'est lui qui
  faisait de `pg_wrapper` un candidat PostgreSQL.
- **Deux fois de suite, c'est l'instance qui a tranché**, pas mes tests : la première correction
  passait chez moi et échouait là-bas. Le test porte désormais le message **exact** de la machine,
  pas une approximation.
- **Vérifié sur l'instance** : `/usr/bin/pg_dump` lit **18.6**, six candidats réels, **aucun**
  en 153.0, et le faux wrapper est écarté.
- **Constat sans gravité** : `/usr/bin/pg_dump` et `/usr/lib/postgresql/18/bin/pg_dump` sont deux
  fichiers distincts — le wrapper et le binaire — donc deux candidats, tous deux en 18.6. C'est
  exact : ce sont bien deux chemins qui fonctionnent.
- `mise run verify` : **0**.

### 2026-09-19 — vague 2, `A-11`

- `ContainerToolFinder` déclaré **par le domaine** comme second port, et `Diagnose` reçoit les deux
  énumérateurs (`N-3`). `resolve.Subject` porte le conteneur ; `internal/cli` le tire de
  `tools.container` et câble l'adaptateur.
- `N-4` tenue : une base qui a déclaré un conteneur **n'emprunte jamais** un outil de l'hôte, même
  quand il y en a un de parfaitement compatible. L'erreur est enveloppée en nommant le conteneur,
  pour qu'on ne la confonde pas avec une absence d'outil.
- **Vérifié contre le vrai conteneur MariaDB de l'instance** : `doctor` annonce
  `mariadb-dump 11.4.13 container` — l'outil du conteneur, exactement à la version de son propre
  serveur. C'est le cas que la stratégie existe pour servir.
- **Limite de la vérification sur machine réelle** : conteneur arrêté, c'est la **base** qui devient
  injoignable en premier, puisqu'elle vit dans le même conteneur — la sonde échoue avant la
  résolution. Le cas « conteneur muet, base joignable » n'est donc couvert que par le test unitaire
  avec un faux, et c'est écrit ici plutôt que passé sous silence.
- `RSV-10` amendée. `mise run verify` : **0**.

### 2026-09-19 — vague 3, `A-10`, et rejeu complet

- **ADR-0015** : un parc mixte MySQL et MariaDB exige la stratégie `exec`. Il **borne** ADR-0014
  sans le rouvrir — `A-10` ne montre pas que l'installation gérée manque, elle montre que `exec`
  en est le complément nécessaire.
- Le conflit a été **reconfirmé deux fois** sur l'instance : installer `mysql-client` **supprime**
  deux paquets MariaDB, et demander les deux explicitement donne
  `mariadb-client-core Conflicts virtual-mysql-client-core`.
- `README` : le conflit, son message exact, et les **trois** configurations que koffr sert.
- **Le rejeu, sur le parc réel** — c'est la démonstration de la décision :

  | Base | Serveur | Outil retenu | Provenance |
  | --- | --- | --- | --- |
  | `boutique` | postgresql 16.15 | `/usr/bin/pg_dump` 18.6 | host |
  | `erp` | mariadb 11.4.13 | `/usr/bin/mariadb-dump` 11.8.6 | host |
  | `crm` | mysql 8.4.11 | `mysqldump` 8.4.11 | **container** |
  | `eteinte` | — | — | injoignable |

  Les trois familles servies **sur une seule machine**, malgré un conflit de paquets qui
  l'interdisait il y a une heure.
- Les sept parcours rejoués **sans contournement** : la version prouvée par exécution (chemin 17,
  binaire 15.4 → **15.4**), le piège du § 5.2 (**16 géré** choisi), le message d'échec, les deux
  modes de `config validate`, et le refus d'`exec` sans conteneur.
- `mise run verify` : **0**.
