# Rétrospective lot 0 — Squelette, outillage et spike des outils

- **Période** : le 2026-09-18, 12 vagues (7 au plan initial, 5 au plan de corrections), 36 commits.
- **Plans** : `docs/plans/lot-0-squelette.md` et `docs/plans/lot-0-corrections.md`.
  **Critère de sortie** : **atteint**, et vérifié sur une machine autre que le poste de
  développement.

## Ce qui s'est passé

Le dépôt est passé de vingt-et-un fichiers Markdown à un produit qui se construit, se vérifie et
s'installe ailleurs : module Go, 20 paquets, 8 règles métier tracées, les 7 tables de `E-028`, les
9 règles d'architecture rendues bloquantes, et trois binaires statiques sous 30 Mo.

Deux choses ont dévié du plan, dans deux directions opposées.

**Le spike `E-130` a mieux fini que prévu.** Le plan tenait l'échec sur Alpine pour acquis et
prévoyait un ADR pour amender `E-043`. Alpine passe — jusqu'à la sauvegarde réelle d'une base par
TCP. `E-043` reste entière, le lot 1 garde sa vague « installation gérée », et l'ADR n'a pas lieu
d'être écrit.

**L'état de départ du plan était faux.** Le dépôt distant nommé par `N-2` contenait déjà 59 commits
d'une autre implémentation de koffr. Arrêt de la vague, `N-12`, décision du propriétaire, reprise.
Rien n'a été poussé avant.

La recette a trouvé **huit** anomalies, dont une bloquante, et a exigé un plan de corrections de
cinq vagues. Le rejeu de ce plan en a trouvé une neuvième — une régression que les corrections
elles-mêmes avaient introduite.

## Ce qui a marché

- **Le spike en premier.** La vague 2 a levé le risque n° 1 du CDC avant que quoi que ce soit ne
  s'appuie dessus. Elle a aussi produit deux contraintes durables (`DT_RUNPATH` ne s'hérite pas,
  `PT_INTERP` est absolu) qui auraient coûté bien plus cher découvertes au lot 1.
- **La fixture violante comme preuve du contrôle.** `internal/arch/testdata/` contient un fichier
  qui casse chaque règle ; si le contrôle cessait de les détecter, le test échoue. Sans cela, un
  contrôle d'architecture ne prouve rien — et j'ai constaté deux fois dans ce lot qu'un test peut
  passer à vide.
- **Rendre un ADR opposable dans le schéma.** ADR-0006 aurait pu rester une page. Les tables
  `STRICT` refusent `"12 MB"` dans une colonne d'entiers, et un `GLOB` refuse `+02:00` dans un
  horodatage. La décision se défend toute seule, sans relecteur.
- **La recette sur une machine qui n'est pas le poste de développement.** Elle a trouvé `A-01`, que
  ni mon poste (Xcode) ni la CI (gcc) ne pouvaient voir. Un lot 0 « vert partout » était en fait
  cassé pour la première personne qui aurait rejoint le projet.
- **Le rejeu après corrections.** Il a trouvé `A-08`. Sans lui, on livrait un lot 0 réputé propre
  qui faisait du bruit à chaque commande.

## Ce qui a coûté

- **L'état de départ du plan n'avait été vérifié qu'en local.** `git rev-parse` échouait, on en a
  conclu « pas de dépôt ». Personne n'a regardé le dépôt **distant** que `N-2` nommait pourtant.
  *Coût* : une vague arrêtée en cours, un arbitrage à demander, et le risque — évité de justesse —
  d'écraser 59 commits publics.
- **Les versions « connues » du modèle étaient périmées.** `actions/checkout@v5` et
  `jdx/mise-action@v3` n'étaient plus les versions courantes (v7 et v4). Vérifié avant de pousser,
  donc sans coût — mais c'est un réflexe à ne pas perdre.
- **Le découpage du plan a rendu le rouge impossible sur deux tâches.** Les tâches `5.3` et une
  partie de `5.4` testaient un schéma et une purge écrits à la tâche précédente. *Coût* : un rouge
  par suppression de contrainte au lieu d'un rouge par antériorité, et une règle de méthode
  improvisée en séance.
- **Deux tests ont passé à vide.** Le contrôle `CFG-06` sur `access_key_id` s'exécutait sur un
  document sans destination ; la garde « aucune connexion sortante » d'ADR-0008 était vraie par
  construction, rien n'utilisant le composeur. *Coût* : deux `omitempty` manquants passés
  inaperçus, et une garde décorative jusqu'à ce que `Gate.Dial` la câble.
- **Le câblage des journaux a été fait sans se demander qui lit.** `E-121` a été appliquée à la
  lettre à une commande CLI, ce qu'elle ne décrit pas. *Coût* : une régression (`A-08`), une vague
  de plus, un ADR.
- **Une vague mergée avant confirmation de sa CI.** Le `watch` avait couru avant que l'exécution
  n'existe et renvoyé 1 ; le merge a suivi quand même. `main` était vert et la vague était
  documentaire, mais la règle a été enfreinte.

## Ce qu'on change

| Quoi | Où | Fait |
| --- | --- | --- |
| L'état de départ d'un plan se vérifie **aussi hors du dépôt local** : dépôt distant, registre de paquets, nom de domaine — tout ce que le plan nomme | `METHODE.md` § Exécution d'un plan | [x] |
| Le **rejeu** du scénario de recette après corrections fait partie de la recette, pas de la politesse | `METHODE.md` § Definition of done d'un lot | [x] |
| Quand le rouge par antériorité est impossible, on le **dit** et on prouve que le test mord en retirant la contrainte | `METHODE.md` § Exécution d'un plan, et skill `implementer` § TDD | [x] |
| Un test qui ne peut pas échouer ne prouve rien : vérifier qu'il **mord**, avec une fixture violante ou en retirant ce qu'il teste | skill `implementer` § TDD | [x] |
| Vérifier la version courante d'une action ou d'un outil externe **avant** de l'épingler | `CLAUDE.md` § Conventions | [x] |
| Attendre la **fin** de la CI avant de merger, en s'assurant que l'exécution surveillée existe bien | `METHODE.md` § Exécution d'un plan | [x] |
| Les pièges de la pile rencontrés au lot 0 (14) | `CLAUDE.md` § Conventions | [x] |
| Destinataires des journaux | ADR-0012 | [x] |
| `main` est protégé contre la destruction ; la verdeur tient par la méthode, à durcir dès qu'on est plus d'un | `ROADMAP.md` § Règles de pilotage | [x] |

### Deux documents à scinder

Appliqué en partie seulement, et dit plutôt que masqué :

- **`CLAUDE.md` fait 269 lignes** pour un seuil de relecture de 150. Le § Conventions (14 pièges) et
  le glossaire métier (45 lignes) en font l'essentiel. Proposition : sortir le glossaire dans
  `docs/glossaire.md` et renvoyer, ce qui ramènerait le fichier autour de 210 lignes — puis déplacer
  les pièges vers le skill `implementer` à mesure qu'ils s'y fondent naturellement.
- **`ROADMAP.md` fait 217 lignes** pour le même seuil. Chaque lot y a un paragraphe de périmètre et
  de critère de sortie, ce que le gabarit n'appelle pas : une roadmap porte « un lot par ligne ».
  Proposition : ramener le corps à la table d'état plus les règles de pilotage, et déplacer les
  paragraphes de périmètre dans le plan de chaque lot, où ils sont déjà repris.

Ni l'un ni l'autre n'est fait ici : ce sont des scissions à décider, pas des corrections. Elles
valent une ligne de `docs/backlog.md` si elles ne sont pas faites au lot 1.

## Pour le kit

Ce qui vaudrait pour tout projet, pas seulement celui-ci :

1. **Le gabarit de plan devrait exiger que l'état de départ couvre l'extérieur du dépôt.** Sa
   section « État de départ » dit « vérifié dans le code et la base » ; elle devrait dire aussi
   « et dans tout ce que le plan nomme : dépôt distant, registre, domaine ».
2. **La Definition of done devrait inclure le rejeu du scénario après corrections.** Une recette
   corrigée mais non rejouée ne voit pas ce que ses propres corrections ont cassé — constaté ici.
3. **Le skill `executer-plan` devrait dire quoi faire quand le rouge par antériorité est
   impossible**, cas fréquent dès qu'une tâche teste ce que la précédente a écrit.
4. **Le gabarit de recette devrait demander une machine distincte du poste de développement.** La
   seule anomalie bloquante du lot était invisible ailleurs.
