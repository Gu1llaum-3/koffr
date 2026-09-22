# Décisions en attente — registre `D-nn`

Les arbitrages de projet qui ne sont pas tranchés : budget, calendrier, périmètre, technique.
Une ligne par décision ; quand elle est prise, la ligne est **barrée** avec la date et le renvoi
(ADR, plan, `rules.md`), jamais supprimée.

| Ce qui va ici                                              | Où va le reste                         |
| ---------------------------------------------------------- | -------------------------------------- |
| Un arbitrage du propriétaire ou de la direction            | —                                      |
| Un choix technique structurant pas encore fait             | — (une fois fait → ADR)                |
| Une question aux utilisateurs (« vous en servez-vous ? »)  | `questions.md`                         |
| Un défaut constaté                                         | `recette/anomalies.md`                 |
| Une idée hors périmètre                                    | `backlog.md`                           |

Les quatre décisions que le CDC renvoie lui-même au propriétaire (§ 11.1) sont `D-02` à `D-05`.

## Registre

| #    | Décision à prendre | Qui tranche | Bloque | Ouverte le | Tranchée le → où |
| ---- | ------------------ | ----------- | ------ | ---------- | ---------------- |
| ~~D-01~~ | Utilisateur référent pour la recette régulière ? | propriétaire | recette du premier lot livrant un écran | 2026-09-18 | **Tranchée le 2026-09-18, provisoirement** : le propriétaire joue la recette sur une instance Multipass. **Amenée à évoluer** |
| ~~D-02~~ | ~~Nom définitif du produit et licence~~ | propriétaire | lot 0 (`go.mod`, en-têtes, dépôt public ou non), `Q-17` | 2026-09-18 | **2026-09-18 → ADR-0001** : `koffr` / `koffr-server`, Apache-2.0 |
| ~~D-03~~ | ~~Déduplication et sauvegarde incrémentale~~ | propriétaire | `E-129`, format d'archive, donc lot de la sauvegarde | 2026-09-18 | **2026-09-18 → ADR-0005** : écartée du produit |
| ~~D-04~~ | ~~Portée de Windows~~ | propriétaire | `E-117`, `Q-20`, matrice de CI | 2026-09-18 | **2026-09-18 → ADR-0011** : hors périmètre, annoncé |
| D-05 | Quand ouvrir le cahier des charges du serveur central | propriétaire | `Q-16`, lot de la liaison | 2026-09-18 | |
| D-08 | Quand livrer les **objets globaux** d'un cluster (`pg_dumpall --globals-only`) : c'est ce qui rend une reprise après sinistre réelle, `Q-07` l'a montré | propriétaire | `B-01`, `E-058`, la promesse de reprise du README | 2026-09-22 | **Ouverte le 2026-09-22 en recette du lot 2** : remontée du backlog, lot à choisir |
| ~~D-06~~ | Qui construit, signe et héberge les binaires d'outils, et à quel coût récurrent | propriétaire | `E-131`, `E-043`, `E-044`, `Q-15` | 2026-09-18 | **Tranchée le 2026-09-19 (ADR-0014)** : installation gérée hors MVP |
| ~~D-07~~ | ~~Version de Go~~ | propriétaire | lot 0 (`go.mod`) | 2026-09-18 | **2026-09-18 → ADR-0002** : 1.27 partout |
| ~~D-08~~ | ~~Le serveur central vit-il dans ce dépôt ou dans un autre ?~~ | propriétaire | organisation du dépôt dès le lot 0, `D-05` | 2026-09-18 | **2026-09-18 → ADR-0001** : dépôts séparés, protocole public |

## Détail

### ~~D-01~~ — Utilisateur référent pour la recette — tranchée **provisoirement** le 2026-09-18

koffr n'a pas d'utilisateur métier : son utilisateur est un exploitant. `METHODE.md` exige une
recette régulière avec un utilisateur réel dès le premier lot qui produit un écran. À défaut d'un
tiers, il faut nommer qui joue ce rôle et sur quel parc réel — une machine de test avec de vraies
bases, pas un jeu de données inventé.

**Tranchée pour l'instant** : le propriétaire joue la recette lui-même, sur une **instance
Multipass** dédiée (`koffr`, Ubuntu 26.04 LTS `arm64`), réinitialisable par snapshot. Elle a servi
à la recette du lot 0 et à son rejeu, et elle a trouvé une anomalie **bloquante** que ni le poste
de développement ni la CI ne voyaient (`A-01`) : le dispositif fait son travail.

**Le propriétaire annonce que cela évoluera.** Ce n'est donc pas une réponse définitive, et la
question se rouvre d'elle-même dès que l'un de ces seuils est franchi :

- **Lot 1** : l'instance devra porter **Docker** et de **vraies bases** PostgreSQL, MySQL et
  MariaDB, en versions différentes — la matrice `N7` d'ADR-0004 en demande neuf. Une instance de
  2 Go de mémoire n'y suffira probablement pas.
- **Lot 7** : le scénario 1 du § 8 chronomètre une installation **par quelqu'un qui découvre
  l'outil**, en suivant la seule documentation. Le propriétaire, qui l'a écrite, ne peut pas jouer
  ce rôle sans biais. Il faudra un tiers.
- **Lot final** : les onze scénarios rejoués sur une machine vierge.

À rouvrir explicitement à la clôture du lot 1, ou plus tôt si le parc de test change.

### ~~D-02~~ — Nom et licence — tranchée le 2026-09-18 (ADR-0001)

**Tranchée** : le produit s'appelle `koffr`, le serveur central `koffr-server` (jamais `koffr-ui`,
qui entrerait en collision avec `koffr ui`, l'interface locale de l'agent), sous Apache-2.0. Reste à
faire au lot 0 : vérifier la disponibilité du nom (organisation GitHub, `pkg.go.dev`, Docker Hub,
Homebrew, domaine).

**Contexte d'origine.**

Le § 11.1 dit que « Keeper » est un nom de travail et que « la licence conditionne la contribution
externe et l'usage commercial ». La décision est structurante tôt : elle fixe le chemin du module Go
(`go.mod`), le nom du binaire, les chemins `/etc/keeper`, `/var/lib/keeper` du § 4.3, les noms des
commandes, et elle détermine la réponse à `Q-17` (langue de l'interface). Un renommage après le lot 1
touche la configuration des utilisateurs.

### ~~D-03~~ — Déduplication — tranchée le 2026-09-18 (ADR-0005)

**Tranchée** : écartée du produit, pas seulement du MVP. Une sauvegarde produit une archive
autonome ; la réintroduire supposerait de rouvrir `E-075` (déchiffrable par `age` standard) et
`E-070` (dépôt exploitable sans l'agent), donc un autre produit.

**Contexte d'origine.**

Le § 10 le dit sans détour : « change entièrement le format de stockage. À décider **avant la v1** si
l'objectif est de concurrencer les outils à dépôt, après si l'objectif est la fiabilité du dump
logique ». Le § 11.1 durcit : « trancher maintenant : elle change le format de stockage et ne se
rattrape pas après coup ». La question sous-jacente est celle du positionnement du produit, pas une
question technique.

### ~~D-04~~ — Portée de Windows — tranchée le 2026-09-18 (ADR-0011)

**Tranchée** : hors périmètre. Trois cibles publiées (`linux/amd64`, `linux/arm64`,
`darwin/arm64`) ; `E-117` amendée ; `Q-20` réduite au seul cas macOS.

**Contexte d'origine.**

`N1` publie un binaire `windows/amd64`. Le § 11.1 constate que « le binaire compile, mais la matrice
de tests et les chemins d'outils doublent le travail » et exige de choisir. Conséquences directes :
la matrice de CI, les chemins de résolution de `F2.1`, la faisabilité de `F2.6` hors ELF (`Q-20`), et
l'unité `systemd` de `N4` qui n'a pas d'équivalent.

### D-05 — Calendrier du CDC du serveur central

Le § 11.1 : « son cahier des charges doit commencer dès que `L3` est engagé, pour que le protocole se
stabilise avec un vrai consommateur en face ». C'est un engagement de calendrier à prendre ou à
écarter ; il conditionne la réponse à `Q-16`.

### ~~D-06~~ — Construction et hébergement des binaires d'outils — tranchée le 2026-09-19 (ADR-0014)

**Tranchée** : troisième option. L'installation gérée sort du MVP ; les sources d'outils sont
l'hôte et le conteneur de la base. Il n'y a plus d'archive à construire, à signer ni à héberger.

**Contexte d'origine.**


`F2.6` et `F2.7` supposent une infrastructure permanente : construire sept versions de PostgreSQL et
trois de MariaDB, sur deux architectures, avec bibliothèques embarquées et `RPATH` ajusté, les
signer, les héberger, épingler leurs empreintes dans chaque version de l'agent, et recommencer à
chaque version amont. Le § 11 qualifie cela de « coût récurrent à assumer ». Pour un projet porté par
une personne, c'est l'engagement le plus lourd du document, et il conditionne une fonctionnalité que
les scénarios 2 et 3 du § 8 rendent obligatoire. Options : l'assumer ; s'appuyer sur des paquets
amont déjà signés en se contentant de les réempaqueter ; ou réduire le MVP à la détection d'outils
présents sur l'hôte, en repoussant l'installation gérée — ce qui contredirait `E-043` et les
scénarios 2 et 3.

### ~~D-07~~ — Version de Go — tranchée le 2026-09-18 (ADR-0002)

**Tranchée** : 1.27 pour l'outillage et pour `go.mod`.

**Contexte d'origine.**

Le CDC dit « Go 1.23+ » ; `mise.toml`, déjà présent dans le dépôt, déclare `go = "1.27"`. Aucune
contradiction formelle, mais un choix posé sans trace. À confirmer dans l'ADR de stack, avec la
version minimale déclarée dans `go.mod` (qui n'est pas forcément celle de l'outillage).

### ~~D-08~~ — Où vit le serveur central — tranchée le 2026-09-18 (ADR-0001)

**Tranchée** : deux dépôts distincts, `koffr` et `koffr-server`. Le protocole vit dans un paquet
public du dépôt de l'agent, que le serveur importera.

**Contexte d'origine.**

Le § 3 place le serveur hors MVP, le § 9 lui donne « un cahier des charges distinct ». Le dépôt
courant est-il celui de l'agent seul, ou un dépôt qui accueillera les deux ? La réponse change
l'arborescence du lot 0, le chemin du module Go et le découpage de la CI. Elle ne change pas le
protocole, qui reste défini côté agent.
