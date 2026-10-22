// Data loaded for one signed-in user.
//
// A Scope lives from sign-in to sign-out. What the workspace loads for the
// user is kept here, and asynchronous work checks after every await that
// its scope is still alive before it touches the page or the workspace. An
// answer that arrives after sign-out, possibly while another user is signed
// in, is thereby dropped instead of showing the previous user's data.

import { FileInfos } from './files.mjs';
import { Profiles } from './profiles.mjs';

export class Scope {
  #alive = true;

  constructor(api) {
    this.profiles = new Profiles(api);
    this.timelines = new Map(); // room_id → Timeline of the rooms opened so far
    this.files = new FileInfos(api);
    this.unread = new Set(); // room_ids with messages the user has not opened yet
  }

  get alive() {
    return this.#alive;
  }

  end() {
    this.#alive = false;
  }
}
