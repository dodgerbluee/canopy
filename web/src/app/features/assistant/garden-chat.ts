import {
  afterNextRender,
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  Injector,
  inject,
  input,
  output,
  signal,
  viewChild,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { AskGardenDocument, type AskGardenMutation } from '@canopy/contracts';
import { GraphQLClient } from '../../core/graphql/client';
import { UiEntryDialog } from '../../ui/entry-dialog';
import { resultData } from '../calendar/calendar-data';
@Component({
  selector: 'garden-chat-dialog',
  imports: [FormsModule, UiEntryDialog],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './garden-chat.html',
  styleUrls: ['../seasons/seasons-dialogs.css'],
})
export class GardenChatDialog {
  readonly open = input(false);
  readonly year = input(new Date().getFullYear());
  readonly dismissed = output<void>();
  private readonly gql = inject(GraphQLClient);
  private readonly injector = inject(Injector);
  readonly chatDialog = viewChild<UiEntryDialog>('chatDialog');
  private alive = true;
  private chatRequest?: AbortController;
  readonly question = signal('');
  readonly asking = signal(false);
  readonly chatError = signal('');
  readonly answer = signal<AskGardenMutation['askGarden'] | null>(null);
  constructor() {
    inject(DestroyRef).onDestroy(() => {
      this.alive = false;
      this.chatRequest?.abort();
    });
  }
  reset(): void {
    this.cancelQuestion();
    this.answer.set(null);
    this.chatError.set('');
  }
  async ask(prompt?: string): Promise<void> {
    if (this.asking()) return;
    if (prompt) this.question.set(prompt);
    if (this.question().trim().length < 3) {
      this.chatError.set('Enter a question first.');
      return;
    }
    this.asking.set(true);
    this.chatError.set('');
    this.answer.set(null);
    this.chatRequest?.abort();
    const request = (this.chatRequest = new AbortController());
    try {
      const answer = resultData(
        await this.gql.mutate(
          AskGardenDocument,
          { question: this.question().trim(), year: this.year() },
          request.signal,
        ),
      ).askGarden;
      if (this.alive && this.chatRequest === request && !request.signal.aborted)
        this.answer.set(answer);
    } catch (e) {
      if (this.alive && this.chatRequest === request && !request.signal.aborted)
        this.chatError.set(e instanceof Error ? e.message : 'Garden chat is unavailable.');
    } finally {
      if (this.alive && this.chatRequest === request) {
        this.asking.set(false);
        afterNextRender(
          () => {
            if (this.alive && this.open() && !this.asking()) this.chatDialog()?.focusFirstField();
          },
          { injector: this.injector },
        );
      }
    }
  }
  cancelQuestion(): void {
    this.chatRequest?.abort();
    this.asking.set(false);
  }
}
