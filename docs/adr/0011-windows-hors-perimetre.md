# ADR-0011 — Windows est hors périmètre, et c'est annoncé

- **Date** : 2026-09-18
- **Statut** : accepté
- **Exigences** : `E-117`, `E-043`, `E-120`, `E-124`, `E-011`
- **Références** : tranche `D-04` ; réduit `Q-20` ; CDC § 7 `N1`, § 11.1 ; lié à ADR-0002

## Contexte

Le CDC laisse une contradiction ouverte et demande de la lever. `N1` (`E-117`) liste
`windows/amd64` parmi les cibles de publication. Le § 11.1 constate pourtant que « le binaire
compile, mais la matrice de tests et les chemins d'outils doublent le travail », et exige un choix
net : « le supporter ou l'annoncer comme non supporté — pas d'entre-deux ».

Le coût réel n'est pas la compilation croisée, qui est gratuite en Go. Il est ailleurs, et il est
permanent :

- les chemins de résolution d'outils de `E-038` sont ceux de distributions Linux et de `PATH` ;
- `E-043` décrit des « bibliothèques partagées embarquées » et un « `RPATH` ajusté » : c'est du
  vocabulaire ELF, qui n'a pas d'équivalent Windows (`Q-20`) ;
- `E-120` livre une unité `systemd` durcie, qui n'existe pas sur Windows ;
- la matrice d'intégration de `E-123`, déjà élargie par ADR-0004 à onze instances de bases,
  doublerait ;
- le risque n° 1 du § 11 se valide sur Debian, Rocky et Alpine : rien n'est prévu pour Windows.

## Décision

Windows est **hors périmètre**, et la documentation l'annonce.

- `E-117` est amendée : les cibles publiées sont `linux/amd64`, `linux/arm64` et `darwin/arm64`.
  La cible `windows/amd64` est retirée ; la CI ne la compile pas et ne publie rien pour elle.
- `darwin/arm64` **reste** une cible : c'est la plateforme de développement, et le produit doit y
  tourner pour être écrit et testé. Cela n'en fait pas une plateforme d'exploitation recommandée.
- `E-043` (installation gérée d'outils) ne vaut que sur Linux. Sur macOS, l'agent se contente des
  outils présents sur l'hôte et de la stratégie `exec`, et `koffr tools install` renvoie une erreur
  explicite. C'est la réponse partielle à `Q-20`, qui reste ouverte pour le seul cas macOS.
- Le `README` et la documentation d'installation annoncent les plateformes supportées, sans
  ambiguïté ni « devrait fonctionner ».

## Conséquences

- La matrice de CI reste à trois cibles de compilation et onze instances de bases. C'est le gain.
- Un utilisateur Windows n'est pas servi. Le cas d'usage — un serveur de bases de données Windows
  sauvegardé par un agent local — existe, en particulier avec SQL Server, que `E-018` met déjà hors
  périmètre. Les deux exclusions se tiennent.
- Rien n'interdit de publier plus tard un binaire Windows : cela demandera un nouvel ADR, des
  chemins de résolution Windows, un service Windows en place de `systemd`, et une extension de la
  matrice de tests. Ce n'est pas un réglage, c'est un lot.
- **Ce qui rouvrirait la décision** : une demande d'utilisateur réel accompagnée d'un parc Windows à
  sauvegarder, ou l'entrée de SQL Server dans le périmètre.

## Alternatives écartées

- **Publier un binaire Windows sans le tester** : c'est l'« entre-deux » que le § 11.1 refuse, et
  cela contredit `P3` et `P4` — promettre ce qu'on ne démontre pas.
- **Supporter Windows pleinement** : double la matrice de tests et impose une seconde stratégie
  d'installation d'outils dès le lot 1, pour un utilisateur qui n'est pas identifié.
- **Retirer aussi `darwin/arm64`** : rendrait le produit non testable sur la machine où il s'écrit.
