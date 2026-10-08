# Rétrospective lot 3 — Manifeste, catalogue et vérification

- **Période** : du 2026-09-22 au 2026-10-08, **douze vagues** (six de plan, six de corrections),
  42 commits, 15 PR.
- **Plan** : `docs/plans/lot-3-manifeste-catalogue-verification.md`, puis
  `docs/plans/lot-3-corrections.md`. **Critère de sortie** : atteint — les six points, dont le
  sixième seulement après les corrections.

## Ce qui s'est passé

Le lot a livré les deux dernières étapes de `E-024` — vérification et manifeste —, le **catalogue
local**, dont les sept tables existaient depuis le lot 0 sans avoir jamais reçu une ligne, et les
commandes `list` et `verify`. Un dépôt d'archives s'inventorie désormais avec `jq` seul et se
restaure depuis une machine sans koffr : la recette l'a fait, 400 000 commandes et 300 000 lignes
rendues avec la seule clé de séquestre, sommes identiques à l'original.

La contradiction du cahier des charges a été tranchée tôt : `F4.2` demande de relire l'archive
**écrite**, qui est chiffrée, et `E-113` interdit à koffr la clé privée. ADR-0017 pose que la
structure se vérifie **au vol**, pendant le dump, le seul instant où koffr voit le clair.

La recette a trouvé **onze anomalies**, dont deux bloquantes. Les corrections en ont découvert une
douzième, `A-30`, la plus grave du lot et antérieure à lui. Le binaire est passé de 13,5 à 17,3 Mio
— SQLite entre par `cmd/koffr` — et n'a plus bougé pendant les corrections.

Écarté comme prévu : `E-065` (revérification périodique, lot 6), `E-068` (échec d'une destination,
lot 4). `Q-02` reste ouverte, portée par `N-5`, et se tranche au lot 4 qui apporte S3.

## Ce qui a marché

- **L'inconnue traitée en premier.** La vague 1 a posé la vérification de structure au vol avant
  tout le reste. Si elle avait été infaisable sans clé privée, le lot entier changeait de forme ;
  on l'a su en une vague au lieu de cinq.
- **Mesurer avant d'écrire le plan.** Trois mesures faites sur l'instance avant la rédaction :
  `pg_restore --list` fonctionne depuis un tube, et **rend 0 sur un flux tronqué à 200 Ko**. Cette
  seule mesure a changé la forme du lot — elle a montré que l'empreinte et la structure attrapent
  **deux défauts différents** et qu'aucune ne remplace l'autre. Elle a resservi deux semaines plus
  tard pour qualifier `A-30`.
- **Les tests retournés plutôt que supprimés.** Six tests des lots 2 et 3 affirmaient l'ancien
  comportement. Aucun n'a été effacé : chacun a été réécrit pour dire la nouvelle règle, ce qui
  laisse dans le dépôt la trace de ce qui a changé et pourquoi.
- **Le marché « le test dépose, le script exécute » » (`N-8` du lot 2), employé une sixième fois**
  pour `check-inventory.sh` : le test Go produit un vrai dépôt, le script l'inventorie avec `jq`
  seul. C'est la seule façon de prouver une promesse qui dit « sans nos outils ».
- **Rejouer la recette sur la machine, pas seulement les tests.** Chaque vague de corrections a été
  vérifiée sur le parc réel avant d'être mergée. C'est là qu'on a vu qu'un job en échec décrivait
  encore une archive inexistante — aucun test ne le regardait.

## Ce qui a coûté

- **Un plan écrit de mémoire au lieu du gabarit.** La première version du plan du lot 3 omettait
  trois sections et, surtout, employait des **listes numérotées au lieu de cases `- [ ]`**, que
  `/executer-plan` lit pour détecter les tâches. Le propriétaire l'a vu avant moi. Coût : le plan
  réécrit en entier. Cause : le skill `ecrire-plan` dit « depuis `docs/plans/0000-template.md` » et
  rien n'obligeait à l'ouvrir.
- **Une mention d'absence semée dans chaque résultat neuf** (`deferredToLot3`), que seul le chemin
  nominal effaçait. Elle a survécu au lot qui l'implémentait et a fait rater le critère de sortie
  n° 6. Cause : une valeur par défaut qui affirme quelque chose, au lieu d'un champ vide qui
  n'affirme rien. **Aucun test ne regardait le chemin d'échec.**
- **Une erreur de fermeture jetée par un `defer`** (`A-30`). `defer func() { _ = dump.Close() }()`
  se lit comme un nettoyage ; c'était la seule lecture du code de sortie du sous-process. Coût :
  une brèche `P4` présente depuis le lot 2, trouvée par hasard en corrigeant autre chose. Cause :
  un `_ =` qui ne dit pas **ce qu'on jette**.
- **Deux fois la même leçon sur la CI.** Attendre l'exécution avant d'ouvrir la PR bloque
  indéfiniment, puisque la CI se déclenche sur `pull_request`. C'était déjà dans `CLAUDE.md`.
- **Une garde de source écrite en `grep` qui s'est signalée elle-même**, deux fois : d'abord sur
  son code, puis sur la **phrase de commentaire** qui explique ce qu'elle interdit. Cause : un
  contrôle qui lit du texte là où il devrait lire un arbre syntaxique.
- **Une migration prévue au plan et inutile.** `N-7` voulait `backups.duration_ms` ; le schéma du
  lot 0 portait déjà `jobs.duration_ms`, `size_bytes` et `finished_at`. Cause : l'état de départ
  avait été vérifié dans le **code**, pas dans le **schéma**, pour ce point précis.
- **Multipass hors service toute la journée du 2026-10-08**, et une instance (`hermes`) détruite
  pour rien : j'avais la bonne hypothèse — le démon ne joint plus ses instances — avant la
  suppression et je ne l'ai pas défendue. Coût : une demi-journée, et une machine qui n'était pas
  la mienne.

## Ce qu'on change

| Quoi | Où | Fait |
| ---- | -- | ---- |
| Un plan s'écrit **en ouvrant le gabarit**, jamais de mémoire ; les tâches sont des cases `- [ ]`, que `/executer-plan` lit | skill `ecrire-plan` § Écrire | [x] |
| Une valeur par défaut n'**affirme** rien : un champ vide dit « pas encore », une mention dit quelque chose et survit à ce qui la rend fausse | `METHODE.md` § Definition of done | [x] |
| `_ = quelque chose.Close()` jette une erreur : dire **laquelle** et pourquoi, ou la lire. Celle d'un sous-process est son code de sortie | `CLAUDE.md` § Conventions | [x] |
| Une garde de source lit l'**arbre syntaxique**, pas le texte : sinon elle se signale sur la phrase qui l'explique | `CLAUDE.md` § Règles d'architecture | [x] |
| L'état de départ se vérifie dans le **schéma** autant que dans le code : une colonne qui existe déjà est une migration qu'on n'écrit pas | skill `ecrire-plan` § Avant d'écrire | [x] |
| Un test de cas nominal ne prouve rien du **chemin d'échec** : une règle qui porte sur ce que le produit dit se teste aussi quand il échoue | skill `implementer` | [x] |
| Une hypothèse de diagnostic qu'on tient se **défend avant** le geste irréversible, pas après | `METHODE.md` § Règles de pilotage | [x] |
| La vérifiabilité est une règle métier, pas une colonne d'écran : `P4` rend une base non vérifiable **non prête** | déjà fait — `RSV-14`, `Diagnosis.Healthy()` | [x] |

## Pour le kit

- **Mesurer l'outil tiers avant de bâtir dessus.** « `pg_restore --list` rend 0 sur un flux
  tronqué » tient en deux minutes de mesure et a décidé la forme d'un lot entier. Un plan qui
  suppose ce que fait un outil externe est un plan qui sera refait.
- **Une valeur par défaut qui affirme quelque chose est une dette.** Elle est écrite une fois et
  relue jamais ; le jour où elle devient fausse, rien ne le signale. Le vide ne ment pas.
- **Rejouer la recette après les corrections, sur la machine.** Trois défauts de ce lot — l'archive
  décrite alors que rien n'a été écrit, la phrase de `verify` qui sur-promettait, la table des
  matières dans le journal — n'étaient visibles qu'à l'écran, pas dans un test.
- **Un contrôle qu'on n'a pas vu échouer ne prouve rien.** Appliqué deux fois dans ce lot : la
  garde de fuseau, avec un fichier planté, et le contrôle d'empreinte du script d'inventaire, avec
  une copie sabotée.
