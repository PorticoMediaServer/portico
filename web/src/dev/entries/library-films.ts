/**
 * The demo movie library as `design/ui-v1/desktop-browse.html` draws it: its eighty films with
 * the years the frame prints, and the runtimes, ratings, watched marks and progress the frame's
 * grid, sort, list and Collections states show. Titles and years come from the frame itself; a
 * film the frame never draws keeps the demo pool's own name and a plausible year so the library
 * still counts eighty. This is fixture data for a DEV specimen, never a server answer.
 *
 * A film is `[title, year, minutes, community rating, genre, watched, progress]`; an absent
 * rating prints an en dash in the list view, and progress is the watched share (0–1).
 */
export type FilmFact = readonly [title: string, year: number, minutes: number, rating: number | undefined, genre: string, watched?: boolean, progress?: number];

/** The library's eighty films, in the order the frame's grid draws them. */
export const films: readonly FilmFact[] = [
  ['12 Angry Men', 1957, 97, 8.5, 'Drama'],
  ['Alien', 1979, 117, 8.2, 'Science Fiction', true],
  ['Aliens', 1986, 137, 7.9, 'Science Fiction'],
  ['Alpha', 2026, 110, undefined, 'Adventure'],
  ['Avatar', 2009, 162, 7.6, 'Science Fiction'],
  ['The Avengers', 2012, 143, 7.7, 'Action'],
  ['Avengers: Endgame', 2019, 181, 8.2, 'Action'],
  ['Avengers: Infinity War', 2018, 149, 8.2, 'Action'],
  ['Back to the Future', 1985, 116, 8.3, 'Science Fiction'],
  ['Backrooms', 2026, 98, undefined, 'Horror'],
  ['Barbie', 2023, 114, 7, 'Comedy'],
  ['Blade Runner', 1982, 117, 7.9, 'Science Fiction'],
  ['Coyote vs. Acme', 2026, 95, undefined, 'Family'],
  ['The Dark Knight', 2008, 152, 8.5, 'Action'],
  ['Drawn Together', 2026, 104, undefined, 'Comedy'],
  ['Dune', 2021, 155, 7.8, 'Science Fiction'],
  ['Dune: Part Two', 2024, 166, 8, 'Science Fiction'],
  ['The Empire Strikes Back', 1980, 124, 8.7, 'Science Fiction'],
  ['The End of Oak Street', 2026, 108, undefined, 'Drama'],
  ['Fight Club', 1999, 139, 8.8, 'Drama'],
  ['The Fix', 2026, 101, undefined, 'Thriller'],
  ['Forrest Gump', 1994, 142, 8.8, 'Drama'],
  ['Gladiator', 2000, 155, 8.5, 'Action'],
  ['The Godfather', 1972, 175, 9.2, 'Drama'],
  ['The Godfather Part II', 1974, 202, 9, 'Drama'],
  ['GoodFellas', 1990, 145, 8.7, 'Drama'],
  ['Grave of the Fireflies', 1988, 89, 8.5, 'Animation'],
  ['The Green Mile', 1999, 189, 8.6, 'Drama'],
  ['Hope', 2026, 132, undefined, 'Science Fiction'],
  ['How to Train Your Dragon', 2010, 98, 8.1, 'Animation'],
  ['Inception', 2010, 148, 8.8, 'Science Fiction'],
  ['The Incredibles', 2004, 115, 8, 'Animation'],
  ['Incredibles 2', 2018, 118, 7.6, 'Animation'],
  ['Inside Out', 2015, 95, 8.2, 'Animation', false, .09],
  ['Interstellar', 2014, 169, 8.7, 'Science Fiction'],
  ['Jaws', 1975, 124, 8.1, 'Thriller'],
  ['Jurassic Park', 1993, 127, 8.2, 'Science Fiction'],
  ['The Lord of the Rings: The Fellowship of the Ring', 2001, 178, 8.9, 'Fantasy'],
  ['The Lord of the Rings: The Return of the King', 2003, 201, 9, 'Fantasy'],
  ['The Lord of the Rings: The Two Towers', 2002, 179, 8.8, 'Fantasy'],
  ['The Matrix', 1999, 136, 8.7, 'Science Fiction'],
  ['Mayday', 2026, 107, undefined, 'Action'],
  ['Moana', 2026, 105, undefined, 'Family'],
  ['Monsters, Inc.', 2001, 92, 8.1, 'Animation'],
  ['Obsession', 2026, 113, undefined, 'Thriller'],
  ['The Odyssey', 2026, 168, undefined, 'Adventure'],
  ["One Flew Over the Cuckoo's Nest", 1975, 133, 8.7, 'Drama'],
  ['One Night Only', 2026, 121, undefined, 'Drama'],
  ['Oppenheimer', 2023, 180, 8.3, 'Drama'],
  ['Parenthood', 1989, 124, 7, 'Comedy'],
  ['Practical Magic 2', 2026, 104, undefined, 'Fantasy'],
  ['Project Hail Mary', 2026, 142, undefined, 'Science Fiction'],
  ['Pulp Fiction', 1994, 154, 8.9, 'Drama'],
  ['Raiders of the Lost Ark', 1981, 115, 8.4, 'Adventure'],
  ['Resident Evil', 2026, 96, undefined, 'Horror'],
  ['Return of the Jedi', 1983, 131, 8.3, 'Science Fiction'],
  ['The Rivals of Amziah King', 2025, 130, undefined, 'Drama'],
  ['Saving Private Ryan', 1998, 169, 8.6, 'Drama'],
  ["Schindler's List", 1993, 195, 9, 'Drama'],
  ['Se7en', 1995, 127, 8.6, 'Thriller'],
  ['The Shawshank Redemption', 1994, 142, 9.3, 'Drama'],
  ['The Shining', 1980, 146, 8.4, 'Horror'],
  ['The Sixth Sense', 1999, 107, 8.2, 'Thriller'],
  ['Spider-Man: Across the Spider-Verse', 2023, 140, 8.6, 'Animation'],
  ['Spider-Man: Brand New Day', 2026, 118, undefined, 'Action'],
  ['Spider-Man: Into the Spider-Verse', 2018, 117, 8.4, 'Animation'],
  ['Spirited Away', 2001, 125, 8.6, 'Animation'],
  ['Star Wars', 1977, 121, 8.6, 'Science Fiction'],
  ['Teenage Sex and Death at Camp Miasma', 2026, 99, undefined, 'Horror'],
  ['The Terminator', 1984, 107, 8.1, 'Action'],
  ['Terminator 2: Judgment Day', 1991, 137, 8.6, 'Action'],
  ['Three Colors: Red', 1994, 99, 8.1, 'Drama'],
  ['Titanic', 1997, 194, 7.9, 'Drama'],
  ['Toy Story', 1995, 81, 8.3, 'Animation'],
  ['Toy Story 2', 1999, 92, 7.9, 'Animation'],
  ['Toy Story 3', 2010, 103, 8.3, 'Animation'],
  ['Toy Story 5', 2026, 96, undefined, 'Animation'],
  ['WALL·E', 2008, 98, 8.4, 'Animation'],
  ['You+Me - Against the World', 2026, 110, undefined, 'Drama'],
  ['Your Name.', 2016, 106, 8.4, 'Animation', true],
];

/** `#big` is the same library at 48,000 films: the frame draws these twenty-one and their count. */
export const bigTitles: readonly string[] = [
  'Jaws', 'Jurassic Park', 'The Lord of the Rings: The Fellowship of the Ring', 'The Lord of the Rings: The Return of the King',
  'The Lord of the Rings: The Two Towers', 'The Matrix', 'Mayday', 'Moana', 'Monsters, Inc.', 'Obsession', 'The Odyssey',
  "One Flew Over the Cuckoo's Nest", 'One Night Only', 'Oppenheimer', 'Parenthood', 'Practical Magic 2', 'Project Hail Mary',
  'Pulp Fiction', 'Raiders of the Lost Ark', 'Resident Evil', 'Return of the Jedi',
];

/** `#list` draws fifteen rows: the frame's own title order, which skips one film the grid has. */
export const listTitles: readonly string[] = [
  '12 Angry Men', 'Alien', 'Aliens', 'Alpha', 'Avatar', 'The Avengers', 'Avengers: Endgame', 'Avengers: Infinity War',
  'Backrooms', 'Barbie', 'Blade Runner', 'Coyote vs. Acme', 'The Dark Knight', 'Drawn Together', 'Dune',
];

/** `#collections` draws these ten, with the films and years each holds. */
export type CollectionFact = Readonly<{name: string; films: number; from: number; to: number; watched?: number}>;
export const collections: readonly CollectionFact[] = [
  {name: 'Alien', films: 2, from: 1979, to: 1986, watched: 1},
  {name: 'The Avengers', films: 3, from: 2012, to: 2019},
  {name: 'Dune', films: 2, from: 2021, to: 2024},
  {name: 'The Godfather', films: 2, from: 1972, to: 1974},
  {name: 'The Incredibles', films: 2, from: 2004, to: 2018},
  {name: 'The Lord of the Rings', films: 3, from: 2001, to: 2003},
  {name: 'Spider-Verse', films: 2, from: 2018, to: 2023},
  {name: 'Star Wars', films: 3, from: 1977, to: 1983},
  {name: 'The Terminator', films: 2, from: 1984, to: 1991},
  {name: 'Toy Story', films: 4, from: 1995, to: 2026},
];
